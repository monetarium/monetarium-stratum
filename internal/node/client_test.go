package node

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/dcrjson"
)

// getWorkSequence returns a getWork stub that yields errs in order and then
// work, counting its calls.
func getWorkSequence(calls *int, errs ...error) func() ([]byte, []byte, error) {
	return func() ([]byte, []byte, error) {
		*calls++
		if *calls <= len(errs) {
			return nil, nil, errs[*calls-1]
		}
		return []byte{1}, nil, nil
	}
}

func TestWaitForNodeWaitsForSyncingNode(t *testing.T) {
	var calls int
	getWork := getWorkSequence(&calls,
		&dcrjson.RPCError{Code: dcrjson.ErrRPCClientNotConnected, Message: "Monetarium is not connected"},
		&dcrjson.RPCError{Code: dcrjson.ErrRPCClientInInitialDownload, Message: "Monetarium is downloading blocks..."},
	)

	if err := waitForNode(context.Background(), slog.Disabled, getWork, time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("getwork called %d times, want 3", calls)
	}
}

func TestWaitForNodeFailsOnConfigError(t *testing.T) {
	// The node refuses getwork while it mines by itself; waiting cannot fix
	// that, so the pool must exit and say why.
	cpuMining := &dcrjson.RPCError{Code: dcrjson.ErrRPCMisc, Message: "getwork polling is disallowed while CPU mining is enabled"}
	var calls int
	getWork := getWorkSequence(&calls, cpuMining)

	err := waitForNode(context.Background(), slog.Disabled, getWork, time.Millisecond)
	if !errors.Is(err, cpuMining) {
		t.Fatalf("got error %v, want it to wrap %v", err, cpuMining)
	}
	if calls != 1 {
		t.Fatalf("getwork called %d times, want 1", calls)
	}
}

func TestWaitForNodeStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	syncing := &dcrjson.RPCError{Code: dcrjson.ErrRPCClientInInitialDownload, Message: "Monetarium is downloading blocks..."}
	getWork := func() ([]byte, []byte, error) {
		cancel()
		return nil, nil, syncing
	}

	err := waitForNode(ctx, slog.Disabled, getWork, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got error %v, want context.Canceled", err)
	}
}
