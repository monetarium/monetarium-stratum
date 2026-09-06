package main

import (
	"encoding/binary"
	"testing"

	"lukechampine.com/blake3"
)

// b3IV is the BLAKE3 initial chaining value.
var b3IV = [8]uint32{
	0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A,
	0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19,
}

func b3Ror(x uint32, n uint) uint32 { return x>>n | x<<(32-n) }

// b3G mirrors the G mix in kernel.cl.
func b3G(v *[16]uint32, a, b, c, d int, x, y uint32) {
	v[a] += v[b] + x
	v[d] = b3Ror(v[d]^v[a], 16)
	v[c] += v[d]
	v[b] = b3Ror(v[b]^v[c], 12)
	v[a] += v[b] + y
	v[d] = b3Ror(v[d]^v[a], 8)
	v[c] += v[d]
	v[b] = b3Ror(v[b]^v[c], 7)
}

// b3Round runs the eight G calls of one BLAKE3 round on the current message
// block.  m is permuted for the next round by the caller (except the last).
func b3Round(v *[16]uint32, m *[16]uint32) {
	b3G(v, 0, 4, 8, 12, m[0], m[1])
	b3G(v, 1, 5, 9, 13, m[2], m[3])
	b3G(v, 2, 6, 10, 14, m[4], m[5])
	b3G(v, 3, 7, 11, 15, m[6], m[7])
	b3G(v, 0, 5, 10, 15, m[8], m[9])
	b3G(v, 1, 6, 11, 12, m[10], m[11])
	b3G(v, 2, 7, 8, 13, m[12], m[13])
	b3G(v, 3, 4, 9, 14, m[14], m[15])
}

// b3Permute applies the BLAKE3 message schedule permutation in place.
func b3Permute(m *[16]uint32) {
	var t [16]uint32 = *m
	m[0] = t[2]
	m[1] = t[6]
	m[2] = t[3]
	m[3] = t[10]
	m[4] = t[7]
	m[5] = t[0]
	m[6] = t[4]
	m[7] = t[13]
	m[8] = t[1]
	m[9] = t[11]
	m[10] = t[12]
	m[11] = t[5]
	m[12] = t[9]
	m[13] = t[14]
	m[14] = t[15]
	m[15] = t[8]
}

// b3Compress is a faithful Go port of blake3_compress in kernel.cl.  It mutates
// m across the seven rounds, so the caller must not reuse m afterwards.
func b3Compress(m *[16]uint32, cv *[8]uint32, counter, blockLen, flags uint32) [8]uint32 {
	var v [16]uint32
	copy(v[0:8], cv[:])
	v[8] = b3IV[0]
	v[9] = b3IV[1]
	v[10] = b3IV[2]
	v[11] = b3IV[3]
	v[12] = counter
	v[13] = 0
	v[14] = blockLen
	v[15] = flags

	for r := 0; r < 7; r++ {
		b3Round(&v, m)
		if r < 6 {
			b3Permute(m)
		}
	}

	var out [8]uint32
	for i := 0; i < 8; i++ {
		v[i] ^= v[i+8]
		v[i+8] ^= cv[i]
	}
	copy(out[:], v[0:8])
	return out
}

// TestMidstateMatchesFullBlake3 proves that compressing header blocks 0 and 1
// once into a chaining value and then compressing block 2 with an injected
// nonce produces the same digest as a full blake3.Sum256 over the header.  This
// is the exact scheme the OpenCL kernel now uses, so it guards against any
// regression in the midstate refactor.
func TestMidstateMatchesFullBlake3(t *testing.T) {
	var hdr [headerLen]byte
	for i := range hdr {
		hdr[i] = byte(i * 7)
	}
	binary.LittleEndian.PutUint32(hdr[nonceOffset:nonceOffset+4], 0xDEADBEEF)

	ref := blake3.Sum256(hdr[:])

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
	b2[3] = binary.LittleEndian.Uint32(hdr[nonceOffset : nonceOffset+4])
	out := b3Compress(&b2, &cv, 0, 52, 0x02|0x08)

	var sum [32]byte
	for i, w := range out {
		binary.LittleEndian.PutUint32(sum[i*4:], w)
	}
	if sum != ref {
		t.Fatalf("midstate scheme mismatch\n got %x\nwant %x", sum, ref)
	}
}