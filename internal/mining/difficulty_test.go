package mining

import (
	"math/big"
	"testing"

	"github.com/monetarium/monetarium-node/blockchain/standalone"
	"github.com/monetarium/monetarium-node/chaincfg"
)

func TestShareTarget(t *testing.T) {
	params := chaincfg.MainNetParams()

	// With difficulty 1 the target must be clamped to the pow limit.
	target := ShareTarget(params, 1)
	if target.Cmp(params.PowLimit) != 0 {
		t.Fatalf("share target for difficulty 1 got %x want %x", target, params.PowLimit)
	}

	// With difficulty D the target must equal powLimit/D.
	diff := uint32(1000)
	target = ShareTarget(params, diff)
	want := new(big.Int).Div(new(big.Int).Set(params.PowLimit), new(big.Int).SetUint64(uint64(diff)))
	if target.Cmp(want) != 0 {
		t.Fatalf("share target mismatch: got %x want %x", target, want)
	}

	// Higher difficulty yields a smaller target.
	target2 := ShareTarget(params, diff*10)
	if target2.Cmp(target) >= 0 {
		t.Fatal("higher difficulty must yield a smaller target")
	}
}

func TestBlockTarget(t *testing.T) {
	params := chaincfg.MainNetParams()
	bits := params.PowLimitBits
	target := BlockTarget(bits)
	want := standalone.CompactToBig(bits)
	if target.Cmp(want) != 0 {
		t.Fatalf("block target mismatch: got %x want %x", target, want)
	}
}
