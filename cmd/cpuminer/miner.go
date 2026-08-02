package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/monetarium/monetarium-node/blockchain/standalone"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"lukechampine.com/blake3"
)

// Header field offsets within the 180 byte serialized block header.  These
// match the wire package and internal/mining/header.go.
const (
	headerLen           = 180
	versionOffset       = 0
	prevBlockOffset     = 4
	partialHeaderOffset = 36
	bitsOffset          = 116
	heightOffset        = 128
	timestampOffset     = 136
	nonceOffset         = 140
	extraNonce1Offset   = 144
	extraNonce2Offset   = 148

	// genTx1Len is the length of the partial header served in mining.notify.
	genTx1Len = headerLen - partialHeaderOffset
)

// Job is a single unit of work to be mined.
type Job struct {
	jobID         string
	height        int64
	header        [headerLen]byte
	ntime         [4]byte
	shareTargetBE [32]byte
	blockTargetBE [32]byte
	solved        atomic.Bool
}

// buildJob reconstructs the serialized block header from the mining.notify
// fields.  The reconstruction mirrors the gominer PrepWork layout documented in
// internal/mining/notify_test.go: version, previous block hash and the partial
// header (genTx1) are concatenated and the miner controlled fields are placed
// at their canonical offsets.
func (m *Miner) buildJob(jobID, prevHashHex, genTx1Hex, versionHex, nbitsHex,
	ntimeHex string) (*Job, error) {

	version, err := hex.DecodeString(versionHex)
	if err != nil || len(version) != 4 {
		return nil, errors.New("invalid version field")
	}
	prevHash, err := hex.DecodeString(prevHashHex)
	if err != nil || len(prevHash) != 32 {
		return nil, errors.New("invalid prevhash field")
	}
	genTx1, err := hex.DecodeString(genTx1Hex)
	if err != nil || len(genTx1) != genTx1Len {
		return nil, errors.New("invalid genTx1 field")
	}
	nbits, err := hex.DecodeString(nbitsHex)
	if err != nil || len(nbits) != 4 {
		return nil, errors.New("invalid nbits field")
	}
	ntime, err := hex.DecodeString(ntimeHex)
	if err != nil || len(ntime) != 4 {
		return nil, errors.New("invalid ntime field")
	}
	extraNonce1, err := hex.DecodeString(m.extraNonce1)
	if err != nil || len(extraNonce1) != 4 {
		return nil, fmt.Errorf("invalid extraNonce1 %q", m.extraNonce1)
	}

	var header [headerLen]byte
	copy(header[versionOffset:], version)
	copy(header[prevBlockOffset:], prevHash)
	copy(header[partialHeaderOffset:], genTx1)
	copy(header[timestampOffset:], ntime)
	copy(header[extraNonce1Offset:], extraNonce1)

	job := &Job{
		jobID:         jobID,
		height:        int64(binary.LittleEndian.Uint32(header[heightOffset : heightOffset+4])),
		header:        header,
		shareTargetBE: toTargetBytes(m.shareTarget()),
		blockTargetBE: toTargetBytes(standalone.CompactToBig(binary.LittleEndian.Uint32(nbits))),
	}
	copy(job.ntime[:], ntime)
	return job, nil
}

// toTargetBytes renders target as a 32 byte big-endian value, matching how the
// pool compares the little-endian blake3 output against the target.
func toTargetBytes(target *big.Int) [32]byte {
	var be [32]byte
	target.FillBytes(be[:])
	return be
}

// cmpHashTarget compares the little-endian blake3 proof of work sum against a
// target's big-endian representation and returns -1, 0 or +1 when the hash is
// less than, equal to or greater than the target.  It mirrors
// standalone.HashToBig(hash).Cmp(target) without allocating a big.Int per hash.
func cmpHashTarget(sum [32]byte, be *[32]byte) int {
	for i := 0; i < len(be); i++ {
		if a, b := sum[31-i], be[i]; a != b {
			if a < b {
				return -1
			}
			return +1
		}
	}
	return 0
}

// classifySolution decides whether a proof of work hash meets the share and/or
// block targets.
func classifySolution(sum [32]byte, shareBE, blockBE *[32]byte) (isShare, isBlock bool) {
	if cmpHashTarget(sum, shareBE) > 0 {
		return false, false
	}
	return true, cmpHashTarget(sum, blockBE) <= 0
}

// hashWorker continuously mines the current job.
func (m *Miner) hashWorker(ctx context.Context, idx int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job := m.currentJob.Load()
		if job == nil {
			m.waitForJob(ctx)
			continue
		}
		if job.solved.Load() {
			m.waitForJob(ctx)
			continue
		}
		gen := m.gen.Load()
		m.mineJob(ctx, job, gen, idx)
	}
}

// waitForJob pauses until new work arrives or the connection is torn down.
func (m *Miner) waitForJob(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
	}
}

// mineJob searches the header nonce and extraNonce2 spaces for solutions to the
// given job.  The thread owns a residue class of the extraNonce2 space so that
// concurrent threads never hash the same (extraNonce2, nonce) pair.  It returns
// when the job generation changes (new work or a block found elsewhere) or the
// job has been solved.
func (m *Miner) mineJob(ctx context.Context, job *Job, gen uint64, idx int) {
	var header [headerLen]byte
	copy(header[:], job.header[:])

	var extraNonce2 [8]byte
	binary.LittleEndian.PutUint64(extraNonce2[:], uint64(idx))
	threads := uint64(m.cfg.Threads)

	nonce := uint32(0)
	for {
		if ctx.Err() != nil || m.gen.Load() != gen || job.solved.Load() {
			return
		}

		binary.LittleEndian.PutUint32(header[nonceOffset:nonceOffset+4], nonce)
		copy(header[extraNonce2Offset:extraNonce2Offset+8], extraNonce2[:])

		sum := blake3.Sum256(header[:])
		m.hashes.Add(1)

		if isShare, isBlock := classifySolution(sum, &job.shareTargetBE,
			&job.blockTargetBE); isShare {
			if isBlock {
				m.blocks.Add(1)
				m.gen.Add(1)
				job.solved.Store(true)
				m.log.Infof("block solution found: job=%s height=%d hash=%s",
					job.jobID, job.height, chainhash.Hash(sum))
			}
			m.onSubmit(job, extraNonce2[:], nonce)
			if isBlock {
				return
			}
		}

		nonce++
		if nonce == 0 {
			// The 2^32 nonce space is exhausted; advance to the next range of
			// the extraNonce2 space owned by this thread.
			base := binary.LittleEndian.Uint64(extraNonce2[:])
			binary.LittleEndian.PutUint64(extraNonce2[:], base+threads)
		}
	}
}

// statsLoop logs periodic hashrate and share statistics.
func (m *Miner) statsLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	prevHashes := uint64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hashes := m.hashes.Load()
			rate := float64(hashes-prevHashes) / 5.0
			prevHashes = hashes
			m.log.Infof("hashrate=%s hashes=%d accepted=%d rejected=%d blocks=%d",
				formatHashrate(rate), hashes, m.accepted.Load(),
				m.rejected.Load(), m.blocks.Load())
		}
	}
}

// submitParams builds the mining.submit parameter list.
func submitParams(worker, jobID string, extraNonce2 []byte, ntime []byte,
	nonce []byte) []string {

	return []string{
		worker,
		jobID,
		hex.EncodeToString(extraNonce2),
		hex.EncodeToString(ntime),
		hex.EncodeToString(nonce),
	}
}

// formatHashrate renders a hashes-per-second value for logging.
func formatHashrate(h float64) string {
	switch {
	case h >= 1e9:
		return fmt.Sprintf("%.2f GH/s", h/1e9)
	case h >= 1e6:
		return fmt.Sprintf("%.2f MH/s", h/1e6)
	case h >= 1e3:
		return fmt.Sprintf("%.2f KH/s", h/1e3)
	default:
		return fmt.Sprintf("%.0f H/s", h)
	}
}

// putUint32LE writes v to b in little endian.
func putUint32LE(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
}
