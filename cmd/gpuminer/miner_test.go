package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"math/rand"
	"testing"
	"time"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/blockchain/standalone"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"github.com/monetarium/monetarium-node/wire"

	"github.com/monetarium/monetarium-stratum/internal/mining"
	"lukechampine.com/blake3"
)

// testLogger returns a logger that writes nowhere.
func testLogger() slog.Logger {
	return slog.NewBackend(io.Discard).Logger("TEST")
}

// newTestMiner creates a miner against the mainnet params with a known
// extraNonce1.
func newTestMiner(t *testing.T) *Miner {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	miner := NewMiner(ctx, MinerConfig{
		Pool:    "127.0.0.1:5550",
		User:    "testworker",
		Net:     chaincfg.MainNetParams(),
		Host:    "./host",
		Kernels: "./cl",
		Log:     testLogger(),
	})
	miner.extraNonce1 = "00000000"
	return miner
}

// testHeader creates a block header with known field values.
func testHeader() *wire.BlockHeader {
	return &wire.BlockHeader{
		Version:      0x20000000,
		PrevBlock:    chainhash.Hash{0x01, 0x02, 0x03},
		MerkleRoot:   chainhash.Hash{0xaa, 0xbb, 0xcc},
		StakeRoot:    chainhash.Hash{0xdd, 0xee, 0xff},
		VoteBits:     0x0001,
		FinalState:   [6]byte{1, 2, 3, 4, 5, 6},
		Voters:       1,
		FreshStake:   0,
		Revocations:  0,
		PoolSize:     10,
		Bits:         0x2100ffff,
		SBits:        0x4000000,
		Height:       42,
		Size:         0,
		Timestamp:    time.Unix(1600000000, 0),
		Nonce:        0,
		ExtraData:    [32]byte{},
		StakeVersion: 7,
	}
}

// makeWorkBlob serializes a header into a 192 byte getwork data blob.
func makeWorkBlob(t *testing.T) []byte {
	t.Helper()
	b, err := testHeader().Bytes()
	if err != nil {
		t.Fatalf("unable to serialize header: %v", err)
	}
	blob := make([]byte, mining.GetworkDataLen)
	copy(blob, b)
	return blob
}

// fakeHost builds a gpuHost whose send() writes into a pipe the test can read,
// and whose channels are driven by the test.  It avoids launching a real OpenCL
// subprocess.
func fakeHost(t *testing.T) (*gpuHost, *bufio.Reader) {
	t.Helper()
	pr, pw := io.Pipe()
	h := &gpuHost{
		stdin:     pw,
		solutions: make(chan solutionMessage, 1),
		progress:  make(chan progressMessage, 16),
		searched:  make(chan searchedMessage, 1),
		done:      make(chan struct{}),
	}
	return h, bufio.NewReader(pr)
}

// readWork reads the next work message written to the host pipe.
func readWork(t *testing.T, r *bufio.Reader) workMessage {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read work: %v", err)
	}
	var w workMessage
	if err := json.Unmarshal([]byte(line), &w); err != nil {
		t.Fatalf("decode work: %v", err)
	}
	return w
}

// TestBuildJobReconstructsHeader verifies that the job reconstructed from the
// mining.notify fields matches the serialized header of the original getwork
// blob.
func TestBuildJobReconstructsHeader(t *testing.T) {
	blob := makeWorkBlob(t)
	work, err := mining.NewWork(blob, "1")
	if err != nil {
		t.Fatalf("unable to create work: %v", err)
	}

	miner := newTestMiner(t)
	job, err := miner.buildJob("1",
		hex.EncodeToString(work.PrevBlock()),
		hex.EncodeToString(work.PartialHeader()),
		hex.EncodeToString(work.Version()),
		hex.EncodeToString(work.Bits()),
		hex.EncodeToString(work.Timestamp()))
	if err != nil {
		t.Fatalf("unable to build job: %v", err)
	}

	if !bytes.Equal(job.header[:], blob[:headerLen]) {
		t.Fatal("reconstructed header does not match the original blob")
	}
	if job.height != 42 {
		t.Fatalf("height got %d want 42", job.height)
	}
}

// TestBuildJobExtraNonce1Placement verifies that a non-zero extraNonce1 lands
// at the canonical offset and the remaining fields are preserved.
func TestBuildJobExtraNonce1Placement(t *testing.T) {
	blob := makeWorkBlob(t)
	work, err := mining.NewWork(blob, "1")
	if err != nil {
		t.Fatalf("unable to create work: %v", err)
	}

	miner := newTestMiner(t)
	miner.extraNonce1 = "deadbeef"
	job, err := miner.buildJob("1",
		hex.EncodeToString(work.PrevBlock()),
		hex.EncodeToString(work.PartialHeader()),
		hex.EncodeToString(work.Version()),
		hex.EncodeToString(work.Bits()),
		hex.EncodeToString(work.Timestamp()))
	if err != nil {
		t.Fatalf("unable to build job: %v", err)
	}

	if !bytes.Equal(job.header[extraNonce1Offset:extraNonce1Offset+4],
		[]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("extraNonce1 placement got %x", job.header[extraNonce1Offset:extraNonce1Offset+4])
	}
	expected := blob[:headerLen]
	expected[extraNonce1Offset], expected[extraNonce1Offset+1] = 0xde, 0xad
	expected[extraNonce1Offset+2], expected[extraNonce1Offset+3] = 0xbe, 0xef
	if !bytes.Equal(job.header[:], expected) {
		t.Fatal("header differs beyond the extraNonce1 field")
	}
}

// TestHeaderHexPlacesExtraNonce2 verifies that the header hex sent to the GPU
// places the rolled extraNonce2 at its canonical offset with a zero nonce.
func TestHeaderHexPlacesExtraNonce2(t *testing.T) {
	miner := newTestMiner(t)
	job, err := miner.buildJob("1",
		hex.EncodeToString(make([]byte, 32)),
		hex.EncodeToString(make([]byte, genTx1Len)),
		"01000000", "ffff001d", "78563412")
	if err != nil {
		t.Fatalf("unable to build job: %v", err)
	}

	hdr, err := hex.DecodeString(job.headerHex(0x1122334455667788))
	if err != nil || len(hdr) != headerLen {
		t.Fatalf("bad header hex: %v len=%d", err, len(hdr))
	}
	if got := binary.LittleEndian.Uint64(hdr[extraNonce2Offset:]); got != 0x1122334455667788 {
		t.Fatalf("extraNonce2 got %x", got)
	}
	if got := binary.LittleEndian.Uint32(hdr[nonceOffset:]); got != 0 {
		t.Fatalf("nonce got %x, want 0", got)
	}
}

// TestShareTargetHexIsLE verifies the share target sent to the GPU is the
// little-endian representation of the target value, matching the kernel's
// comparison of blake3 output words against target words.
func TestShareTargetHexIsLE(t *testing.T) {
	miner := newTestMiner(t)
	miner.setDifficulty(1)

	job, err := miner.buildJob("1",
		hex.EncodeToString(make([]byte, 32)),
		hex.EncodeToString(make([]byte, genTx1Len)),
		"01000000", "ffff001d", "78563412")
	if err != nil {
		t.Fatalf("unable to build job: %v", err)
	}

	le, err := hex.DecodeString(job.shareTargetHex())
	if err != nil || len(le) != 32 {
		t.Fatalf("bad target hex: %v len=%d", err, len(le))
	}
	// Rebuild the big.Int from the little-endian bytes and compare to PowLimit.
	var rev [32]byte
	for i := 0; i < 32; i++ {
		rev[i] = le[31-i]
	}
	got := new(big.Int).SetBytes(rev[:])
	if got.Cmp(miner.cfg.Net.PowLimit) != 0 {
		t.Fatalf("share target got %s want powlimit %s", got, miner.cfg.Net.PowLimit)
	}
}

// TestShareTargetMatchesPool verifies the miner's share target math matches the
// pool for the difficulties the pool serves.
func TestShareTargetMatchesPool(t *testing.T) {
	params := chaincfg.MainNetParams()
	for _, diff := range []uint32{1, 2, 100, 4096} {
		miner := newTestMiner(t)
		miner.setDifficulty(float64(diff))
		got := miner.shareTarget()
		want := mining.ShareTarget(params, diff)
		if got.Cmp(want) != 0 {
			t.Fatalf("diff %d: target got %s want %s", diff, got, want)
		}
	}
}

// TestClassifySolution verifies the share/block decision logic.
func TestClassifySolution(t *testing.T) {
	var shareBE, blockBE [32]byte
	big.NewInt(100).FillBytes(shareBE[:])
	big.NewInt(50).FillBytes(blockBE[:])

	above := sumLE(big.NewInt(200))
	between := sumLE(big.NewInt(60))
	below := sumLE(big.NewInt(40))

	if isShare, isBlock := classifySolution(above, &shareBE, &blockBE); isShare || isBlock {
		t.Fatalf("hash above share target: share=%v block=%v", isShare, isBlock)
	}
	if isShare, isBlock := classifySolution(between, &shareBE, &blockBE); !isShare || isBlock {
		t.Fatalf("share-only hash: share=%v block=%v", isShare, isBlock)
	}
	if isShare, isBlock := classifySolution(below, &shareBE, &blockBE); !isShare || !isBlock {
		t.Fatalf("block hash: share=%v block=%v", isShare, isBlock)
	}
}

// sumLE converts a value into the little-endian 32 byte form produced by
// blake3, i.e. sum[31] is the most significant byte.
func sumLE(v *big.Int) [32]byte {
	var be [32]byte
	v.FillBytes(be[:])
	var sum [32]byte
	for i := 0; i < 32; i++ {
		sum[31-i] = be[i]
	}
	return sum
}

// TestCmpHashTargetMatchesBigInt verifies the allocation-free byte comparison
// produces the same ordering as big.Int comparison against HashToBig.
func TestCmpHashTargetMatchesBigInt(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	max := new(big.Int).Lsh(big.NewInt(1), 256)
	targets := []*big.Int{
		new(big.Int),
		new(big.Int).Sub(max, big.NewInt(1)),
		new(big.Int).Lsh(big.NewInt(1), 224),
		new(big.Int).Div(chaincfg.MainNetParams().PowLimit, big.NewInt(1177)),
	}
	for i := 0; i < 10; i++ {
		targets = append(targets, new(big.Int).Rand(rng, max))
	}

	for _, target := range targets {
		be := toTargetBytes(target)
		for i := 0; i < 100; i++ {
			var sum [32]byte
			rng.Read(sum[:])
			hash := chainhash.Hash(sum)
			want := standalone.HashToBig(&hash).Cmp(target)
			if got := cmpHashTarget(sum, &be); got != want {
				t.Fatalf("target=%v sum=%x: cmp got %d want %d", target, sum, got, want)
			}
		}
	}
}

// TestSubmitParams verifies the mining.submit parameter list format.
func TestSubmitParams(t *testing.T) {
	params := submitParams("worker1", "7",
		[]byte{1, 2, 3, 4, 5, 6, 7, 8},
		[]byte{0x78, 0x56, 0x34, 0x12},
		[]byte{0xff, 0xee, 0xdd, 0xcc})

	want := []string{"worker1", "7", "0102030405060708", "78563412", "ffeeddcc"}
	if len(params) != len(want) {
		t.Fatalf("params got %v want %v", params, want)
	}
	for i := range want {
		if params[i] != want[i] {
			t.Fatalf("param %d got %q want %q", i, params[i], want[i])
		}
	}
}

// TestBuildJobInvalidFields verifies that malformed notify fields are rejected.
func TestBuildJobInvalidFields(t *testing.T) {
	miner := newTestMiner(t)
	_, err := miner.buildJob("1", "zz", "", "", "", "")
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
	_, err = miner.buildJob("1", "010203", "", "", "", "")
	if err == nil {
		t.Fatal("expected error for short prevhash")
	}
	_, err = miner.buildJob("1",
		hex.EncodeToString(make([]byte, 32)),
		hex.EncodeToString(make([]byte, 10)), "", "", "")
	if err == nil {
		t.Fatal("expected error for short genTx1")
	}
}

// TestMineJobRollsExtraNonce2 verifies that when a full 2^32 sweep completes
// without a solution, the same job is re-sent with the next extraNonce2 value.
func TestMineJobRollsExtraNonce2(t *testing.T) {
	miner := newTestMiner(t)
	host, reader := fakeHost(t)
	miner.host = host

	job := &Job{
		jobID:         "1",
		height:        1,
		shareTargetBE: toTargetBytes(big.NewInt(1)),
		blockTargetBE: [32]byte{},
	}
	// The header template must have valid field sizes so headerHex works.
	copy(job.header[versionOffset:versionOffset+4], []byte{1, 0, 0, 0})
	copy(job.header[prevBlockOffset:prevBlockOffset+32], make([]byte, 32))
	copy(job.header[partialHeaderOffset:], make([]byte, genTx1Len))
	copy(job.ntime[:], []byte{0x78, 0x56, 0x34, 0x12})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		miner.mineJob(ctx, job, 0)
	}()

	// First work message carries extraNonce2 = 0.
	w := readWork(t, reader)
	hdr, err := hex.DecodeString(w.Header)
	if err != nil || len(hdr) != headerLen {
		t.Fatalf("bad header: %v len=%d", err, len(hdr))
	}
	if got := binary.LittleEndian.Uint64(hdr[extraNonce2Offset:]); got != 0 {
		t.Fatalf("first sweep extraNonce2 got %d want 0", got)
	}

	// Simulate a complete sweep with no solution.
	host.searched <- searchedMessage{NoncesChecked: 1 << 32}

	// Second work message must carry extraNonce2 = 1.
	w = readWork(t, reader)
	hdr, err = hex.DecodeString(w.Header)
	if err != nil || len(hdr) != headerLen {
		t.Fatalf("bad header: %v len=%d", err, len(hdr))
	}
	if got := binary.LittleEndian.Uint64(hdr[extraNonce2Offset:]); got != 1 {
		t.Fatalf("second sweep extraNonce2 got %d want 1", got)
	}
	if miner.hashes.Load() != 1<<32 {
		t.Fatalf("hashes got %d want %d", miner.hashes.Load(), uint64(1)<<32)
	}

	cancel()
	<-done
}

// TestMineJobSubmitsSolution verifies that a solution from the host advances to
// the next extraNonce2 sweep, keeping the GPU busy after a find.
func TestMineJobSubmitsSolution(t *testing.T) {
	miner := newTestMiner(t)
	host, reader := fakeHost(t)
	miner.host = host

	job := &Job{
		jobID:         "7",
		height:        1,
		shareTargetBE: toTargetBytes(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))),
		blockTargetBE: [32]byte{},
	}
	copy(job.header[versionOffset:versionOffset+4], []byte{1, 0, 0, 0})
	copy(job.header[prevBlockOffset:prevBlockOffset+32], make([]byte, 32))
	copy(job.header[partialHeaderOffset:], make([]byte, genTx1Len))
	copy(job.ntime[:], []byte{0x78, 0x56, 0x34, 0x12})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		miner.mineJob(ctx, job, 0)
	}()

	// Consume the first work message, then feed a solution.
	_ = readWork(t, reader)
	host.solutions <- solutionMessage{Nonce: 42, NoncesChecked: 64}

	// After the solution the miner rolls to the next extraNonce2 and sends a
	// new sweep, which unblocks the pipe write; drain it.
	w := readWork(t, reader)
	hdr, err := hex.DecodeString(w.Header)
	if err != nil || len(hdr) != headerLen {
		t.Fatalf("bad header: %v len=%d", err, len(hdr))
	}
	if got := binary.LittleEndian.Uint64(hdr[extraNonce2Offset:]); got != 1 {
		t.Fatalf("post-solution extraNonce2 got %d want 1", got)
	}

	cancel()
	<-done
}

// TestHashTargetMatchesPool verifies that converting the blake3 output the way
// the miner does produces the same target the pool computes.
func TestHashTargetMatchesPool(t *testing.T) {
	header := testHeader()
	b, err := header.Bytes()
	if err != nil {
		t.Fatalf("unable to serialize header: %v", err)
	}
	sum := blake3.Sum256(b)
	hash := chainhash.Hash(sum)
	got := standalone.HashToBig(&hash)

	parsed := *header
	pow := parsed.PowHashV2()
	want := standalone.HashToBig(&pow)
	if got.Cmp(want) != 0 {
		t.Fatalf("hash target got %s want %s", got, want)
	}
}
