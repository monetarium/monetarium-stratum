// Copyright (c) 2026 The Monetarium developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package stratum

import (
	"testing"
	"time"

	"github.com/monetarium/monetarium-stratum/internal/mining"
)

// TestSlowClientDoesNotStallWorkDelivery checks that a client which stops
// reading its connection cannot stall work delivery to every other miner.
// enqueue used to block once the slow client's send queue was full, and the
// caller of enqueue is the pool's work delivery loop, so one stalled miner
// (a subscription is enough, no authorization required) stopped new work
// from reaching any miner until it was removed by hand.
func TestSlowClientDoesNotStallWorkDelivery(t *testing.T) {
	server := newTestServer(t, 1, mining.AcceptedShare, nil)
	stalled := newTestClient(server)
	fast := newTestClient(server)

	// Fill the stalled client's send queue without ever reading from its
	// pipe, as a miner that subscribed and went away would.
	for len(stalled.sends) < cap(stalled.sends) {
		stalled.sends <- []byte("x")
	}

	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	// Broadcasting work to all clients must complete promptly...
	done := make(chan struct{})
	go func() {
		server.BroadcastWork()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("work delivery stalled by a client that is not reading")
	}

	// ...the stalled client must be disconnected...
	for i := 0; i < 100; i++ {
		if server.clientCount() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := server.clientCount(); got != 1 {
		t.Fatalf("server has %d clients after the broadcast, want 1 (the stalled client disconnected)", got)
	}

	// ...and the healthy client must have received the work.
	select {
	case <-fast.sends:
	default:
		// The queue has room; the message must already be there.
		t.Fatal("healthy client did not receive the work notification")
	}
}
