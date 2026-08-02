package mining

import "github.com/monetarium/monetarium-node/wire"

const (
	// MaxBlockHeaderPayload is the maximum number of bytes of a serialized
	// block header.
	MaxBlockHeaderPayload = wire.MaxBlockHeaderPayload

	// GetworkDataLen is the length of the getwork data blob.  It consists of
	// the serialized block header followed by the blake3 padding needed to
	// bring the data length to a multiple of the blake3 block size.  The
	// header is 180 bytes and the next multiple of 64 is 192 bytes.
	GetworkDataLen = 192

	// Canonical offsets within the serialized header and getwork data blob.
	// These are defined in wire/blockheader.go and are NOT the same as the
	// stale comments in the node's rpcserver getwork handler, which are off by
	// one byte.
	versionOffset     = 0
	prevBlockOffset   = 4
	merkleRootOffset  = 36
	stakeRootOffset   = 68
	voteBitsOffset    = 100
	finalStateOffset  = 102
	votersOffset      = 108
	freshStakeOffset  = 110
	revocationsOffset = 112
	poolSizeOffset    = 113
	bitsOffset        = 116
	sBitsOffset       = 120
	heightOffset      = 128
	sizeOffset        = 132
	timestampOffset   = 136
	nonceOffset       = 140
	extraDataOffset   = 144
	stakeVersionOff   = 176

	// extraNonce1 is stored in the first 4 bytes of the header extra data
	// field and extraNonce2 immediately follows it.
	extraNonce1Size = 4
	extraNonce2Size = 8
)

// Slice constants for the getwork data blob.
var (
	// The previous block hash is a copy of the raw hash bytes of the previous
	// block (the exact order produced by the hash function, which is NOT the
	// reversed order block explorers typically display).
	prevBlockSlice = sliceRange{prevBlockOffset, merkleRootOffset}

	// The partial header is everything after the previous block hash up to the
	// end of the header.  It is repurposed as the generate transaction (genTx1)
	// field of the stratum work notification.
	partialHeaderSlice = sliceRange{merkleRootOffset, wire.MaxBlockHeaderPayload}

	// bitsSlice is the compact difficulty bits field.
	bitsSlice = sliceRange{bitsOffset, bitsOffset + 4}

	// timestampSlice is the header timestamp.
	timestampSlice = sliceRange{timestampOffset, timestampOffset + 4}

	// nonceSlice is the header nonce.
	nonceSlice = sliceRange{nonceOffset, nonceOffset + 4}

	// extraNonce1Slice and extraNonce2Slice are the two parts of the header
	// extra data field used for pool work.
	extraNonce1Slice = sliceRange{extraDataOffset, extraDataOffset + extraNonce1Size}
	extraNonce2Slice = sliceRange{extraDataOffset + extraNonce1Size, extraDataOffset + extraNonce1Size + extraNonce2Size}
)

// sliceRange is a half-open byte range [start, end).
type sliceRange struct {
	start, end int
}

// Work is the pool job representation of a getwork data blob received from the
// node.  It exposes the various fields required to both notify miners of new
// work and to reconstruct a solved block header when a miner submits work.
type Work struct {
	jobID  string
	height int64
	data   [GetworkDataLen]byte
}

// NewWork parses a getwork data blob into a Work struct.
func NewWork(data []byte, jobID string) (*Work, error) {
	if len(data) != GetworkDataLen {
		return nil, makeErr("work data has invalid length: got %d, want %d",
			len(data), GetworkDataLen)
	}
	w := &Work{jobID: jobID}
	copy(w.data[:], data)
	w.height = int64(u32LE(w.data[heightOffset : heightOffset+4]))
	return w, nil
}

// JobID returns the job identifier.
func (w *Work) JobID() string { return w.jobID }

// Height returns the block height of the work.
func (w *Work) Height() int64 { return w.height }

// PrevBlock returns the raw previous block hash bytes.
func (w *Work) PrevBlock() []byte { return w.data[prevBlockSlice.start:prevBlockSlice.end] }

// PartialHeader returns the partial header used as the stratum generate
// transaction field (genTx1).
func (w *Work) PartialHeader() []byte {
	return w.data[partialHeaderSlice.start:partialHeaderSlice.end]
}

// Version returns the block version in little endian.
func (w *Work) Version() []byte {
	return w.data[versionOffset : versionOffset+4]
}

// Bits returns the compact difficulty bits in little endian.
func (w *Work) Bits() []byte { return w.data[bitsSlice.start:bitsSlice.end] }

// Timestamp returns the header timestamp in little endian.
func (w *Work) Timestamp() []byte { return w.data[timestampSlice.start:timestampSlice.end] }

// Data returns a copy of the full getwork data blob.
func (w *Work) Data() []byte {
	data := make([]byte, GetworkDataLen)
	copy(data, w.data[:])
	return data
}

// BuildSolvedHeader applies the miner submission fields (extra nonce 1, extra
// nonce 2, timestamp and nonce) to the getwork data blob and parses the result
// into a block header.
func (w *Work) BuildSolvedHeader(extraNonce1 []byte, extraNonce2 []byte,
	timestamp []byte, nonce []byte) (*wire.BlockHeader, error) {

	if len(extraNonce1) > extraNonce1Size {
		return nil, makeErr("extraNonce1 length of %d is too large", len(extraNonce1))
	}
	if len(extraNonce2) != extraNonce2Size {
		return nil, makeErr("extraNonce2 length is %d, want %d", len(extraNonce2),
			extraNonce2Size)
	}
	if len(timestamp) != 4 {
		return nil, makeErr("timestamp length is %d, want 4", len(timestamp))
	}
	if len(nonce) != 4 {
		return nil, makeErr("nonce length is %d, want 4", len(nonce))
	}

	data := make([]byte, GetworkDataLen)
	copy(data, w.data[:])
	copy(data[extraNonce1Slice.start:extraNonce1Slice.end], extraNonce1)
	copy(data[extraNonce2Slice.start:extraNonce2Slice.end], extraNonce2)
	copy(data[timestampSlice.start:timestampSlice.end], timestamp)
	copy(data[nonceSlice.start:nonceSlice.end], nonce)

	var header wire.BlockHeader
	err := header.FromBytes(data[:wire.MaxBlockHeaderPayload])
	if err != nil {
		return nil, err
	}
	return &header, nil
}

// SolvedHeaderData applies the miner submission fields to a copy of the
// getwork data blob and returns it as the hex encoded string expected by the
// node's getwork submission RPC.
func (w *Work) SolvedHeaderData(extraNonce1 []byte, extraNonce2 []byte,
	timestamp []byte, nonce []byte) (string, error) {

	header, err := w.BuildSolvedHeader(extraNonce1, extraNonce2, timestamp, nonce)
	if err != nil {
		return "", err
	}
	headerBytes, err := header.Bytes()
	if err != nil {
		return "", err
	}
	data := make([]byte, GetworkDataLen)
	copy(data, headerBytes)
	return hexEncode(data), nil
}

func u32LE(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
