package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
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
		Threads: 1,
		Log:     testLogger(),
	})
	miner.extraNonce1 = "00000000"
	return miner
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
	// Everything outside the extra data field must match the original header.
	expected := blob[:headerLen]
	expected[extraNonce1Offset], expected[extraNonce1Offset+1] = 0xde, 0xad
	expected[extraNonce1Offset+2], expected[extraNonce1Offset+3] = 0xbe, 0xef
	if !bytes.Equal(job.header[:], expected) {
		t.Fatal("header differs beyond the extraNonce1 field")
	}
}

// TestBuildJobPowEquivalence verifies that hashing the reconstructed header
// with blake3 produces the same proof of work hash as wire.PowHashV2.
func TestBuildJobPowEquivalence(t *testing.T) {
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

	// Apply a miner controlled nonce and extraNonce2 to the header.
	header := job.header
	binary.LittleEndian.PutUint32(header[nonceOffset:nonceOffset+4], 0x11223344)
	copy(header[extraNonce2Offset:extraNonce2Offset+8], []byte{1, 2, 3, 4, 5, 6, 7, 8})

	sum := blake3.Sum256(header[:])
	var parsed wire.BlockHeader
	if err := parsed.FromBytes(header[:]); err != nil {
		t.Fatalf("unable to parse reconstructed header: %v", err)
	}
	if pow := parsed.PowHashV2(); chainhash.Hash(sum) != pow {
		t.Fatalf("pow hash mismatch: miner=%s wire=%s", chainhash.Hash(sum), pow)
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

// TestMineJobFindsShare verifies that a thread submits a solution when the hash
// meets the share target and that its extraNonce2 is drawn from its assigned
// range.
func TestMineJobFindsShare(t *testing.T) {
	miner := newTestMiner(t)

	type submission struct {
		extraNonce2 [8]byte
		nonce       uint32
	}
	submitted := make(chan submission, 1)
	miner.onSubmit = func(job *Job, extraNonce2 []byte, nonce uint32) {
		var en2 [8]byte
		copy(en2[:], extraNonce2)
		select {
		case submitted <- submission{en2, nonce}:
		default:
		}
	}

	// A target covering the entire hash space guarantees a solution on the
	// first nonce; a zero block target means it is never a block.
	fullTarget := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	job := &Job{
		jobID:         "1",
		height:        1,
		shareTargetBE: toTargetBytes(fullTarget),
		blockTargetBE: [32]byte{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		miner.mineJob(ctx, job, 0, 3)
	}()

	select {
	case s := <-submitted:
		if got := binary.LittleEndian.Uint64(s.extraNonce2[:]); got != 3 {
			t.Fatalf("extraNonce2 base got %d want 3", got)
		}
		if s.nonce != 0 {
			t.Fatalf("nonce got %d want 0", s.nonce)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no share submitted")
	}

	cancel()
	<-done
	if miner.hashes.Load() == 0 {
		t.Fatal("expected at least one hash to be counted")
	}
}

// TestMineJobAbortsOnGenerationChange verifies that a thread stops hashing when
// the job generation changes (new work or a block found elsewhere).
func TestMineJobAbortsOnGenerationChange(t *testing.T) {
	miner := newTestMiner(t)
	job := &Job{
		jobID:         "1",
		height:        1,
		shareTargetBE: toTargetBytes(big.NewInt(1)),
		blockTargetBE: [32]byte{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		miner.mineJob(ctx, job, 0, 0)
	}()

	// Bump the generation to simulate new work; the thread must return.
	time.Sleep(10 * time.Millisecond)
	miner.gen.Add(1)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("miner did not abort on generation change")
	}
	cancel()
}

// TestHashTargetMatchesPool verifies that converting the blake3 output the way
// the miner does produces the same target the pool computes in evaluate.go.
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
