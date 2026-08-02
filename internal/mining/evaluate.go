package mining

import (
	"math/big"

	"github.com/monetarium/monetarium-node/blockchain/standalone"
	"github.com/monetarium/monetarium-node/wire"
)

// Decision is the outcome of evaluating a submitted block header.
type Decision int

const (
	// Rejected indicates the submitted work does not meet the pool share
	// difficulty.
	Rejected Decision = iota

	// AcceptedShare indicates the work meets the share difficulty but not the
	// network block difficulty.  It is accepted as a share without a network
	// submission.
	AcceptedShare

	// SubmitBlock indicates the work solves a block and must be submitted to
	// the network.
	SubmitBlock
)

// EvaluateSubmit decides how solved work should be handled given its proof of
// work hash and the pool share and network block targets.
func EvaluateSubmit(hashTarget *big.Int, shareTarget *big.Int,
	blockTarget *big.Int) Decision {

	if hashTarget.Cmp(shareTarget) > 0 {
		return Rejected
	}
	if hashTarget.Cmp(blockTarget) > 0 {
		return AcceptedShare
	}
	return SubmitBlock
}

// EvaluateHeader computes the proof of work hash of a solved header and
// decides how it should be handled.
func EvaluateHeader(header *wire.BlockHeader, shareTarget *big.Int) Decision {
	powHash := header.PowHashV2()
	hashTarget := standalone.HashToBig(&powHash)
	blockTarget := standalone.CompactToBig(header.Bits)
	return EvaluateSubmit(hashTarget, shareTarget, blockTarget)
}

// HeaderHashTarget returns the big integer proof of work hash of a header.
func HeaderHashTarget(header *wire.BlockHeader) *big.Int {
	powHash := header.PowHashV2()
	return standalone.HashToBig(&powHash)
}
