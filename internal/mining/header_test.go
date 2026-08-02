package mining

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"github.com/monetarium/monetarium-node/wire"
)

// testHeader creates a block header with known field values.
func testHeader(t *testing.T) *wire.BlockHeader {
	t.Helper()
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
func makeWorkBlob(t *testing.T, header *wire.BlockHeader) []byte {
	t.Helper()
	b, err := header.Bytes()
	if err != nil {
		t.Fatalf("unable to serialize header: %v", err)
	}
	blob := make([]byte, GetworkDataLen)
	copy(blob, b)
	return blob
}

func TestNewWorkInvalidLength(t *testing.T) {
	_, err := NewWork(make([]byte, 180), "1")
	if err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestWorkFieldAccessors(t *testing.T) {
	header := testHeader(t)
	work, err := NewWork(makeWorkBlob(t, header), "1")
	if err != nil {
		t.Fatalf("unable to create work: %v", err)
	}

	if got, want := work.JobID(), "1"; got != want {
		t.Fatalf("job id got %q want %q", got, want)
	}
	if got, want := work.Height(), int64(42); got != want {
		t.Fatalf("height got %d want %d", got, want)
	}

	// Verify the previous block hash bytes match the header.
	if !bytes.Equal(work.PrevBlock(), header.PrevBlock[:]) {
		t.Fatalf("prev block mismatch: got %x", work.PrevBlock())
	}
	// Verify the version bytes match.
	if !bytes.Equal(work.Version(), []byte{0x00, 0x00, 0x00, 0x20}) {
		t.Fatalf("version mismatch: got %x", work.Version())
	}
	// Verify the bits bytes match.
	if !bytes.Equal(work.Bits(), []byte{0xff, 0xff, 0x00, 0x21}) {
		t.Fatalf("bits mismatch: got %x", work.Bits())
	}
	// Verify the timestamp bytes match (1600000000 = 0x5f5e1000, LE).
	if !bytes.Equal(work.Timestamp(), []byte{0x00, 0x10, 0x5e, 0x5f}) {
		t.Fatalf("timestamp mismatch: got %x", work.Timestamp())
	}

	// The partial header must be the serialized bytes after the previous block
	// hash, i.e. bytes [36:180].
	serialized, _ := header.Bytes()
	wantPartial := serialized[36:180]
	if !bytes.Equal(work.PartialHeader(), wantPartial) {
		t.Fatalf("partial header mismatch")
	}
}

func TestWorkBuildSolvedHeader(t *testing.T) {
	header := testHeader(t)
	work, err := NewWork(makeWorkBlob(t, header), "1")
	if err != nil {
		t.Fatalf("unable to create work: %v", err)
	}

	extraNonce1, _ := hex.DecodeString("deadbeef")
	extraNonce2, _ := hex.DecodeString("0102030405060708")
	timestamp, _ := hex.DecodeString("78563412")
	nonce, _ := hex.DecodeString("ffeeddcc")

	solved, err := work.BuildSolvedHeader(extraNonce1, extraNonce2, timestamp, nonce)
	if err != nil {
		t.Fatalf("unable to build solved header: %v", err)
	}

	// The original header fields must be preserved.
	if solved.Version != header.Version {
		t.Fatalf("version got %x want %x", solved.Version, header.Version)
	}
	if solved.PrevBlock != header.PrevBlock {
		t.Fatalf("prev block mismatch")
	}
	if solved.MerkleRoot != header.MerkleRoot {
		t.Fatalf("merkle root mismatch")
	}
	if solved.Height != header.Height {
		t.Fatalf("height got %d want %d", solved.Height, header.Height)
	}

	// The applied fields must match.  Numeric fields are little endian on the
	// wire, so writing bytes ffeeddcc parses as nonce 0xccddeeff.
	if solved.Timestamp.Unix() != int64(0x12345678) {
		t.Fatalf("timestamp got %d want %d", solved.Timestamp.Unix(), int64(0x12345678))
	}
	if solved.Nonce != 0xccddeeff {
		t.Fatalf("nonce got %x want %x", solved.Nonce, uint32(0xccddeeff))
	}
	if !bytes.Equal(solved.ExtraData[:4], extraNonce1) {
		t.Fatalf("extraNonce1 got %x", solved.ExtraData[:4])
	}
	if !bytes.Equal(solved.ExtraData[4:12], extraNonce2) {
		t.Fatalf("extraNonce2 got %x", solved.ExtraData[4:12])
	}
}

func TestWorkSolvedHeaderData(t *testing.T) {
	header := testHeader(t)
	work, err := NewWork(makeWorkBlob(t, header), "1")
	if err != nil {
		t.Fatalf("unable to create work: %v", err)
	}

	extraNonce1, _ := hex.DecodeString("deadbeef")
	extraNonce2, _ := hex.DecodeString("0102030405060708")
	timestamp, _ := hex.DecodeString("78563412")
	nonce, _ := hex.DecodeString("ffeeddcc")

	dataHex, err := work.SolvedHeaderData(extraNonce1, extraNonce2, timestamp, nonce)
	if err != nil {
		t.Fatalf("unable to build submission data: %v", err)
	}
	if len(dataHex) != GetworkDataLen*2 {
		t.Fatalf("submission hex length got %d want %d", len(dataHex), GetworkDataLen*2)
	}

	// The submission must round trip into the same header.
	decoded, err := hex.DecodeString(dataHex)
	if err != nil {
		t.Fatalf("unable to decode submission: %v", err)
	}
	var roundTrip wire.BlockHeader
	if err := roundTrip.FromBytes(decoded[:wire.MaxBlockHeaderPayload]); err != nil {
		t.Fatalf("unable to parse submission header: %v", err)
	}
	if roundTrip.Nonce != 0xccddeeff {
		t.Fatalf("round trip nonce got %x", roundTrip.Nonce)
	}
	if !bytes.Equal(roundTrip.ExtraData[:4], extraNonce1) {
		t.Fatalf("round trip extraNonce1 got %x", roundTrip.ExtraData[:4])
	}
}

func TestWorkBuildSolvedHeaderBadExtraNonce1(t *testing.T) {
	header := testHeader(t)
	work, err := NewWork(makeWorkBlob(t, header), "1")
	if err != nil {
		t.Fatalf("unable to create work: %v", err)
	}
	_, err = work.BuildSolvedHeader([]byte("too-long"), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for oversized extraNonce1")
	}
}
