package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lukechampine.com/blake3"
)

/*
 * End-to-end integration tests for the CUDA backend running against the fake
 * CUDA driver + fake NVRTC shared objects (cuda/test).  These compile the real,
 * unmodified production cuda_host binary and execute it with LD_LIBRARY_PATH
 * pointing at the fakes, exercising the whole dlopen/dlsym/cuSym(_v2)/launch
 * path with no GPU, no CUDA toolkit and no NVRTC.
 *
 * Every assertion is cross-checked against an independent Go reference: the
 * midstate scheme (b3Compress) already proven equivalent to blake3.Sum256 by
 * TestMidstateMatchesFullBlake3, applied to the header returned by the host.
 */

var fakeCudaDir = filepath.Join("cuda", "test")

func haveCompiler(args ...string) bool {
	for _, c := range args {
		if _, err := exec.LookPath(c); err != nil {
			return false
		}
	}
	return true
}

func requireToolchain(t *testing.T) {
	t.Helper()
	if !haveCompiler("cc", "g++") {
		t.Skip("cc/g++ not available; fake CUDA integration tests require a C toolchain")
	}
}

// buildFakesAndHost compiles the fake libs and the production host binary into
// buildDir.  Returns the host path and the lib dir for LD_LIBRARY_PATH.
func buildFakesAndHost(t *testing.T, buildDir string) (hostPath, libDir string) {
	t.Helper()
	libDir = filepath.Join(buildDir, "libs")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir libs: %v", err)
	}

	cudaD := func(name string) string { return filepath.Join(fakeCudaDir, name) }

	cc := func(out string, srcs ...string) {
		t.Helper()
		args := append([]string{"-std=c11", "-Wall", "-O2", "-fPIC", "-shared",
			"-o", out}, srcs...)
		cmd := exec.Command("cc", args...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cc %v: %v\n%s", args, err, b)
		}
	}

	cc(filepath.Join(libDir, "libcuda.so.1"),
		cudaD("fake_cuda.c"), cudaD("cpu_reference.c"))
	cc(filepath.Join(libDir, "libnvrtc.so.12"), cudaD("fake_nvrtc.c"))

	out := filepath.Join(buildDir, "cuda_host")
	args := []string{"-std=c++11", "-O2", "-Icuda", "cuda/host.cpp", "-ldl",
		"-lpthread", "-o", out}
	cmd := exec.Command("g++", args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("g++ %v: %v\n%s", args, err, b)
	}
	return out, libDir
}

// runHost starts cuda_host with the fake libs on LD_LIBRARY_PATH, feeds it msg
// (a JSON line) and returns stdout/stderr/exit information after EOF on stdin
// triggers a clean shutdown or after the timeout.
func runHost(t *testing.T, hostPath, libDir string, env []string, msg string) (
	stdout, stderr string, exitErr error) {
	t.Helper()
	cmd := exec.Command(hostPath, "cuda", "0")
	cmd.Env = append(os.Environ(),
		"LD_LIBRARY_PATH="+libDir,
	)
	cmd.Env = append(cmd.Env, env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start host: %v", err)
	}
	if msg != "" {
		stdin.Write([]byte(msg + "\n"))
	}
	stdin.Close()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case exitErr = <-done:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("cuda_host hung; stderr: %s", errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exitErr
}

// rawMessageLines returns each parsed JSON object keyed by type with the raw
// line preserved, so numeric fields (which arrive as float64 via interface{})
// keep their original textual representation.
func rawMessageLines(out string) map[string]string {
	byType := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		if typ, ok := obj["type"].(string); ok {
			byType[typ] = line
		}
	}
	return byType
}

// parseMessages splits stdout into JSON messages by type, stringifying values.
func parseMessages(t *testing.T, out string) map[string]map[string]string {
	t.Helper()
	msgs := make(map[string]map[string]string)
	for typ, line := range rawMessageLines(out) {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		m := make(map[string]string)
		for k, v := range obj {
			m[k] = fmt.Sprintf("%v", v)
		}
		msgs[typ] = m
	}
	return msgs
}

/* ------------------------- test vectors ------------------------- */

func fakeCudaHeader() [headerLen]byte {
	var hdr [headerLen]byte
	for i := range hdr {
		hdr[i] = byte((i*7 + 11) & 0xFF)
	}
	return hdr
}

func fakeCudaTarget(topZero int) [32]byte {
	var tgt [32]byte
	for i := range tgt {
		tgt[i] = 0xFF
	}
	for b := 0; b < topZero; b++ {
		tgt[31-b] = 0x00
	}
	return tgt
}

// referenceLowestNonce computes the lowest matching nonce in [from,to) for the
// midstate scheme, using the same Go reference as midstate_test.go.
func referenceLowestNonce(t *testing.T, hdr [headerLen]byte, tgt [32]byte,
	from, to uint32) (nonce uint32, hash [32]byte) {
	t.Helper()
	var cv [8]uint32 = b3IV
	var m [16]uint32
	for i := 0; i < 16; i++ {
		m[i] = binary.LittleEndian.Uint32(hdr[0+i*4:])
	}
	cv = b3Compress(&m, &cv, 0, 64, 0x01)
	for i := 0; i < 16; i++ {
		m[i] = binary.LittleEndian.Uint32(hdr[64+i*4:])
	}
	cv = b3Compress(&m, &cv, 0, 64, 0x00)
	var b2 [16]uint32
	for i := 0; i < 13; i++ {
		b2[i] = binary.LittleEndian.Uint32(hdr[128+i*4:])
	}
	b2[3] = 0

	var tw [8]uint32
	for i := 0; i < 8; i++ {
		tw[i] = binary.LittleEndian.Uint32(tgt[i*4:])
	}
	for n := from; n < to && n != 0; n++ {
		mm := b2
		mm[3] = n
		out := b3Compress(&mm, &cv, 0, 52, 0x02|0x08)
		ok := true
		for i := 7; i >= 0; i-- {
			if out[i] > tw[i] {
				ok = false
				break
			}
			if out[i] < tw[i] {
				break
			}
		}
		if ok {
			for i, w := range out {
				binary.LittleEndian.PutUint32(hash[i*4:], w)
			}
			return n, hash
		}
	}
	return 0, hash
}

/* ------------------------- tests ------------------------- */

// TestCudaBackendFindsSolution drives the real host against the fake driver
// with a target that has exactly one solution in the first batch (nonce 435,
// from the precomputed vector suite) and cross-checks the reported header
// against blake3.Sum256 and the Go midstate reference.
func TestCudaBackendFindsSolution(t *testing.T) {
	requireToolchain(t)
	buildDir := t.TempDir()
	host, libDir := buildFakesAndHost(t, buildDir)

	hdr := fakeCudaHeader()
	tgt := fakeCudaTarget(1)
	work := workMessage{
		Type:   "work",
		Header: hex.EncodeToString(hdr[:]),
		Target: hex.EncodeToString(tgt[:]),
	}
	payload, _ := json.Marshal(work)

	stdout, stderr, err := runHost(t, host, libDir, nil, string(payload))
	if err != nil {
		t.Fatalf("host failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "CPU CUDA Test Device") {
		t.Fatalf("fake device not detected; stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "compute capability: 8.9") {
		t.Fatalf("compute capability missing; stderr:\n%s", stderr)
	}

	msgs := parseMessages(t, stdout)
	sol, ok := msgs["solution"]
	if !ok {
		t.Fatalf("no solution; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if n, _ := sol["nonce"]; n != "435" {
		t.Fatalf("solution nonce got %q want 435", n)
	}

	gotHdr, err := hex.DecodeString(sol["header"])
	if err != nil || len(gotHdr) != headerLen {
		t.Fatalf("bad solution header: %v len %d", err, len(gotHdr))
	}
	if n := binary.LittleEndian.Uint32(gotHdr[nonceOffset : nonceOffset+4]); n != 435 {
		t.Fatalf("nonce field got %d want 435", n)
	}

	// cross-check the solution hash two ways
	refNonce, refHash := referenceLowestNonce(t, hdr, tgt, 1, 1<<12)
	if refNonce != 435 {
		t.Fatalf("reference lowest nonce got %d want 435", refNonce)
	}
	if sum := blake3.Sum256(gotHdr); sum != refHash {
		t.Fatalf("blake3(header) %x != reference hash %x", sum, refHash)
	}
}

// TestCudaBackendMultipleCandidates ensures multiple matching nonces in a batch
// still resolve deterministically to the lowest one (the reference answer), and
// that the chosen hash is the actual blake3 digest of the header.
func TestCudaBackendMultipleCandidates(t *testing.T) {
	requireToolchain(t)
	buildDir := t.TempDir()
	host, libDir := buildFakesAndHost(t, buildDir)

	hdr := fakeCudaHeader()
	tgt := fakeCudaTarget(2)
	wantNonce, _ := referenceLowestNonce(t, hdr, tgt, 1, 1<<20)
	if wantNonce == 0 {
		t.Fatal("reference found no solution; invalid test setup")
	}

	// cap the per-launch scan so the batch is fully covered in one launch
	env := []string{"FAKE_CUDA_MAX_NONCES=1048576"}
	work := workMessage{
		Type:   "work",
		Header: hex.EncodeToString(hdr[:]),
		Target: hex.EncodeToString(tgt[:]),
	}
	payload, _ := json.Marshal(work)

	stdout, stderr, err := runHost(t, host, libDir, env, string(payload))
	if err != nil {
		t.Fatalf("host failed: %v\nstderr: %s", err, stderr)
	}
	msgs := parseMessages(t, stdout)
	sol, ok := msgs["solution"]
	if !ok {
		t.Fatalf("no solution; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	got, _ := sol["nonce"]
	if got != fmt.Sprintf("%d", wantNonce) {
		t.Fatalf("solution nonce got %s want %d", got, wantNonce)
	}

	gotHdr, err := hex.DecodeString(sol["header"])
	if err != nil || len(gotHdr) != headerLen {
		t.Fatalf("bad solution header: %v len %d", err, len(gotHdr))
	}
	const wantHash = "2d73dd7df4b87f9191351076f485e233476708149b0aa79ddb3b93474ad90000"
	if sum := fmt.Sprintf("%x", blake3.Sum256(gotHdr)); sum != wantHash {
		t.Fatalf("hash got %s want %s", sum, wantHash)
	}
}

// TestCudaBackendNoMatchUsesSearched exercises the full 2^32 sweep path with a
// capped per-launch scan, asserting the host reports searched/nonces_checked
// and exits cleanly instead of finding a bogus solution.
func TestCudaBackendNoMatchUsesSearched(t *testing.T) {
	requireToolchain(t)
	buildDir := t.TempDir()
	host, libDir := buildFakesAndHost(t, buildDir)

	hdr := fakeCudaHeader()
	var tgt [32]byte // all-zero: nothing can ever match
	work := workMessage{
		Type:   "work",
		Header: hex.EncodeToString(hdr[:]),
		Target: hex.EncodeToString(tgt[:]),
	}
	payload, _ := json.Marshal(work)

	env := []string{"FAKE_CUDA_MAX_NONCES=65536"}
	stdout, _, err := runHost(t, host, libDir, env, string(payload))
	if err != nil {
		t.Fatalf("host failed: %v", err)
	}
	raw := rawMessageLines(stdout)
	if _, ok := raw["solution"]; ok {
		t.Fatalf("unexpected solution for impossible target:\n%s", stdout)
	}
	searched, ok := raw["searched"]
	if !ok {
		t.Fatalf("no searched message:\n%s", stdout)
	}
	if !strings.Contains(searched, `"nonces_checked":4294967296`) {
		t.Fatalf("searched message wrong:\n%s", searched)
	}
	if _, ok := raw["progress"]; !ok {
		t.Fatalf("no progress messages:\n%s", stdout)
	}
}

// TestCudaBackendNvrtcCompileFail injects a compile failure through the fake
// NVRTC and verifies the unmodified host reports it and exits non-zero instead
// of silently falling back to a lower arch success.
func TestCudaBackendNvrtcCompileFail(t *testing.T) {
	requireToolchain(t)
	buildDir := t.TempDir()
	host, libDir := buildFakesAndHost(t, buildDir)

	stdout, stderr, err := runHost(t, host, libDir,
		[]string{"FAKE_NVRTC_FAIL_COMPILE=1"}, "")
	if err == nil {
		t.Fatalf("host should fail on nvrtc compile error; stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "nvrtcCompileProgram failed") {
		t.Fatalf("expected nvrtcCompileProgram failure; stderr:\n%s", stderr)
	}
}

// TestCudaBackendCudaErrorPropagates injects a failure in cuMemAlloc via the
// fake driver and verifies the host surfaces the exact error name and aborts.
func TestCudaBackendCudaErrorPropagates(t *testing.T) {
	requireToolchain(t)
	buildDir := t.TempDir()
	host, libDir := buildFakesAndHost(t, buildDir)

	stdout, stderr, err := runHost(t, host, libDir,
		[]string{"FAKE_CUDA_FAIL_NEXT=mem_alloc:2"}, "")
	if err == nil {
		t.Fatalf("host should fail on cuMemAlloc error; stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "cuMemAlloc result failed") {
		t.Fatalf("expected cuMemAlloc failure; stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "CUDA_ERROR_OUT_OF_MEMORY") {
		t.Fatalf("expected error name; stderr:\n%s", stderr)
	}
}

// TestCudaBackendCleanShutdown verifies the host exits 0 and cleans up when
// stdin closes without any work, exercising teardown on the fake driver.
func TestCudaBackendCleanShutdown(t *testing.T) {
	requireToolchain(t)
	buildDir := t.TempDir()
	host, libDir := buildFakesAndHost(t, buildDir)

	stdout, stderr, err := runHost(t, host, libDir, nil, "")
	if err != nil {
		t.Fatalf("host should exit 0 on clean shutdown: %v\nstderr: %s",
			err, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("unexpected stdout on shutdown:\n%s", stdout)
	}
}
