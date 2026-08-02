package mining

import (
	"math/big"
	"testing"

	"github.com/monetarium/monetarium-node/blockchain/standalone"
)

func TestEvaluateSubmit(t *testing.T) {
	bigTarget := new(big.Int).Lsh(big.NewInt(1), 250)
	blockTarget := new(big.Int).Rsh(bigTarget, 20)
	shareTarget := new(big.Int).Rsh(bigTarget, 2)

	tests := []struct {
		name  string
		hash  *big.Int
		share *big.Int
		block *big.Int
		want  Decision
	}{
		{
			name:  "hash exceeds share target",
			hash:  new(big.Int).Add(shareTarget, big.NewInt(1)),
			share: shareTarget,
			block: blockTarget,
			want:  Rejected,
		},
		{
			name:  "hash between share and block target",
			hash:  new(big.Int).Add(blockTarget, big.NewInt(1)),
			share: shareTarget,
			block: blockTarget,
			want:  AcceptedShare,
		},
		{
			name:  "hash below block target",
			hash:  new(big.Int).Sub(blockTarget, big.NewInt(1)),
			share: shareTarget,
			block: blockTarget,
			want:  SubmitBlock,
		},
		{
			name:  "hash equal to share target accepted",
			hash:  new(big.Int).Set(shareTarget),
			share: shareTarget,
			block: blockTarget,
			want:  AcceptedShare,
		},
		{
			name:  "hash equal to block target is a block",
			hash:  new(big.Int).Set(blockTarget),
			share: shareTarget,
			block: blockTarget,
			want:  SubmitBlock,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateSubmit(tc.hash, tc.share, tc.block)
			if got != tc.want {
				t.Fatalf("EvaluateSubmit got %v want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluateHeader(t *testing.T) {
	// With a block target that exceeds any possible hash, every header must
	// evaluate to SubmitBlock as long as the share target is at least the
	// proof of work limit.
	header := testHeader(t)
	header.Bits = 0x2e7fffff
	powLimit := standalone.CompactToBig(0x2e7fffff)
	if powLimit.Sign() < 0 {
		t.Fatalf("test target must be positive, got %x", powLimit)
	}
	if powLimit.Cmp(new(big.Int).Lsh(big.NewInt(1), 256)) <= 0 {
		t.Fatalf("test target must exceed max hash, got %x", powLimit)
	}
	shareTarget := new(big.Int).Lsh(big.NewInt(1), 256)

	if got := EvaluateHeader(header, shareTarget); got != SubmitBlock {
		t.Fatalf("expected SubmitBlock, got %v", got)
	}
}
