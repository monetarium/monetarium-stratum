package mining

import "sync"

// BlockThrottle implements optional self-limiting of block submissions to the
// network.  It allows a pool operator whose hardware finds a disproportionate
// share of blocks to discard some of the solved blocks found, thereby reducing
// their effective submitted hashrate.
//
// Semantics: with a divisor of N, only 1 in every N solved blocks is submitted
// to the node; the remaining N-1 are silently discarded and counted as
// throttled.  A divisor of 1 disables throttling entirely.
type BlockThrottle struct {
	mu          sync.Mutex
	divisor     uint32
	foundBlocks uint64
	submitted   uint64
	throttled   uint64
}

// NewBlockThrottle creates a new block throttle with the provided divisor.  A
// divisor of 0 is treated as 1 (throttling disabled).
func NewBlockThrottle(divisor uint32) *BlockThrottle {
	if divisor == 0 {
		divisor = 1
	}
	return &BlockThrottle{divisor: divisor}
}

// Divisor returns the configured divisor.
func (t *BlockThrottle) Divisor() uint32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.divisor
}

// Allow is called for every solved block.  It reports whether the block should
// be submitted to the network.  When it returns false, the block has been
// discarded by the throttle.
func (t *BlockThrottle) Allow() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.foundBlocks++
	if t.divisor <= 1 || t.foundBlocks%uint64(t.divisor) == 0 {
		t.submitted++
		return true
	}
	t.throttled++
	return false
}

// Stats returns the cumulative found, submitted and throttled block counts.
func (t *BlockThrottle) Stats() (found uint64, submitted uint64, throttled uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.foundBlocks, t.submitted, t.throttled
}
