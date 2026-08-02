package mining

import (
	"math/big"

	"github.com/monetarium/monetarium-node/blockchain/standalone"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
)

// ShareTarget returns the target that hashes must be less than or equal to in
// order to be accepted as a pool share at the given difficulty.  The target is
// computed as powLimit/difficulty and clamped to the proof of work limit.
func ShareTarget(net *chaincfg.Params, difficulty uint32) *big.Int {
	target := new(big.Int).Div(new(big.Int).Set(net.PowLimit),
		new(big.Int).SetUint64(uint64(difficulty)))
	if target.Cmp(net.PowLimit) > 0 {
		target = new(big.Int).Set(net.PowLimit)
	}
	return target
}

// BlockTarget returns the network block target for the given compact bits.
func BlockTarget(bits uint32) *big.Int {
	return standalone.CompactToBig(bits)
}

// HashTarget returns the big integer representation of a proof of work hash.
func HashTarget(powHash *chainhash.Hash) *big.Int {
	return standalone.HashToBig(powHash)
}

// CalcWork converts a big integer target to a work ratio suitable for logging.
func CalcWork(powLimit, target *big.Int) *big.Rat {
	if target.Sign() <= 0 {
		return new(big.Rat)
	}
	return new(big.Rat).Quo(new(big.Rat).SetInt(powLimit),
		new(big.Rat).SetInt(target))
}
