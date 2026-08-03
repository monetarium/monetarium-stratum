package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/monetarium/monetarium-node/chaincfg"
)

// TestConnectAndMineStopsOnCancel verifies that cancelling the miner context
// while connected unwinds the connection instead of hanging.  A fake server
// completes the handshake and then goes silent, leaving the miner blocked in a
// read.  Cancelling the context must close the socket and return.
func TestConnectAndMineStopsOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unable to listen: %v", err)
	}
	defer ln.Close()

	handshakeDone := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var req struct {
				ID     uint64 `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(line, &req); err != nil {
				return
			}
			var resp []byte
			switch req.Method {
			case methodSubscribe:
				resp, err = json.Marshal(map[string]interface{}{
					"id":     req.ID,
					"result": []interface{}{[]interface{}{}, "00000000", float64(8)},
				})
			case methodAuthorize:
				resp, err = json.Marshal(map[string]interface{}{
					"id":     req.ID,
					"result": true,
				})
				if err == nil {
					close(handshakeDone)
				}
			default:
				continue
			}
			if err != nil {
				return
			}
			conn.Write(append(resp, '\n'))
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	miner := NewMiner(ctx, MinerConfig{
		Pool:    ln.Addr().String(),
		User:    "testworker",
		Net:     chaincfg.MainNetParams(),
		Threads: 1,
		Log:     testLogger(),
	})

	done := make(chan error, 1)
	go func() {
		done <- miner.connectAndMine()
	}()

	select {
	case <-handshakeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handshake did not complete")
	}

	cancel()

	select {
	case <-done:
		// Returning at all is the regression check; before the fix the
		// connection was never closed and this hung forever.
	case <-time.After(2 * time.Second):
		t.Fatal("connectAndMine did not return after cancellation")
	}
}
