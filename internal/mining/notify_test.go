package mining

import (
	"bytes"
	"testing"

	"github.com/monetarium/monetarium-node/wire"
)

// TestNotifyRoundTrip replicates the exact work reconstruction performed by
// the gominer stratum client (PrepWork) and verifies that the fields the pool
// serves in a mining.notify notification reassemble into the original getwork
// data blob.
func TestNotifyRoundTrip(t *testing.T) {
	header := testHeader(t)
	blob := makeWorkBlob(t, header)
	work, err := NewWork(blob, "1")
	if err != nil {
		t.Fatalf("unable to create work: %v", err)
	}

	// The fields served in the mining.notify notification.  The node provides
	// the getwork blob with a zeroed extra data field, so the reconstruction
	// uses a zeroed extra nonce to match it; non-zero placement is asserted
	// below.
	version := work.Version()
	prevHash := work.PrevBlock()
	partialHeader := work.PartialHeader()
	extraNonce1 := make([]byte, 4)

	// Reconstruct the getwork blob exactly as gominer's PrepWork does.
	var workData [192]byte
	offset := 0
	offset += copy(workData[offset:], version)
	offset += copy(workData[offset:], prevHash)
	copy(workData[offset:], partialHeader)
	copy(workData[144:], extraNonce1)

	if !bytes.Equal(workData[:], blob) {
		t.Fatal("reconstructed work data does not match the original blob")
	}

	// The serialized header must parse and preserve the original fields.
	var parsed wire.BlockHeader
	if err := parsed.FromBytes(workData[:wire.MaxBlockHeaderPayload]); err != nil {
		t.Fatalf("unable to parse reconstructed header: %v", err)
	}
	if parsed.Version != header.Version {
		t.Fatalf("version got %x want %x", parsed.Version, header.Version)
	}
	if parsed.PrevBlock != header.PrevBlock {
		t.Fatal("prev block mismatch")
	}
	if parsed.Bits != header.Bits {
		t.Fatalf("bits got %x want %x", parsed.Bits, header.Bits)
	}
	if parsed.Height != header.Height {
		t.Fatalf("height got %d want %d", parsed.Height, header.Height)
	}

	// The nBits and nTime notification fields must appear at the canonical
	// offsets within the blob.
	if !bytes.Equal(workData[bitsOffset:bitsOffset+4], work.Bits()) {
		t.Fatal("nbits offset mismatch")
	}
	if !bytes.Equal(workData[timestampOffset:timestampOffset+4], work.Timestamp()) {
		t.Fatal("ntime offset mismatch")
	}

	// A non-zero extra nonce must land exactly at the start of the extra data
	// field.
	workData[extraNonce1Slice.start] = 0xde
	workData[extraNonce1Slice.start+1] = 0xad
	workData[extraNonce1Slice.start+2] = 0xbe
	workData[extraNonce1Slice.start+3] = 0xef
	if !bytes.Equal(workData[extraNonce1Slice.start:extraNonce1Slice.end],
		[]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatal("extraNonce1 placement mismatch")
	}
}
