package stratum

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"github.com/monetarium/monetarium-node/wire"

	"github.com/monetarium/monetarium-stratum/internal/mining"
)

// fakeSubmitter records submissions made to the node.
type fakeSubmitter struct {
	mu       sync.Mutex
	calls    []string
	accepted bool
	err      error
}

func (f *fakeSubmitter) SubmitWork(data string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, data)
	return f.accepted, f.err
}

func (f *fakeSubmitter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSubmitter) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

func testLogger() slog.Logger {
	return slog.NewBackend(io.Discard).Logger("TEST")
}

// makeWorkBlob builds a 192 byte getwork blob from a fresh header.
func makeWorkBlob(t *testing.T) []byte {
	t.Helper()
	header := &wire.BlockHeader{
		Version:      0x20000000,
		PrevBlock:    chainhash.Hash{0x01, 0x02, 0x03},
		MerkleRoot:   chainhash.Hash{0xaa, 0xbb, 0xcc},
		StakeRoot:    chainhash.Hash{0xdd, 0xee, 0xff},
		VoteBits:     0x0001,
		FinalState:   [6]byte{1, 2, 3, 4, 5, 6},
		Bits:         0x2100ffff,
		SBits:        0x4000000,
		Height:       42,
		Timestamp:    time.Unix(1600000000, 0),
		StakeVersion: 7,
	}
	b, err := header.Bytes()
	if err != nil {
		t.Fatalf("unable to serialize header: %v", err)
	}
	blob := make([]byte, mining.GetworkDataLen)
	copy(blob, b)
	return blob
}

// newTestServer creates a server with a stub evaluator that always returns the
// provided decision.
func newTestServer(t *testing.T, divisor uint32, decision mining.Decision,
	submitter Submitter) *Server {

	server := NewServer(context.Background(), &Config{
		Net:                chaincfg.MainNetParams(),
		ShareDifficulty:    100,
		BlockSubmitDivisor: divisor,
		MaxClients:         10,
		Log:                testLogger(),
	}, submitter)
	server.evaluate = func(hashTarget, shareTarget, blockTarget *big.Int) mining.Decision {
		return decision
	}
	return server
}

// newTestClient creates a client bound to the server with a known worker name.
func newTestClient(server *Server) *Client {
	serverConn, clientConn := net.Pipe()
	client := newClient(server, serverConn)
	client.worker = "miner"
	_ = clientConn
	return client
}

// submitRequest builds a parsed submit request.
func submitRequest(id uint64, params []string) *Request {
	raw, _ := json.Marshal(map[string]interface{}{
		"id":     id,
		"method": Submit,
		"params": params,
	})
	req, err := ParseRequest(raw)
	if err != nil {
		panic(err)
	}
	return req
}

// readResponse reads the next response from the client sends channel.
func readResponse(t *testing.T, client *Client) (result interface{}, err *StratumError) {
	t.Helper()
	select {
	case raw := <-client.sends:
		var resp struct {
			Result interface{}   `json:"result"`
			Error  *StratumError `json:"error"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(raw), &resp); err != nil {
			t.Fatalf("unable to parse response %s: %v", raw, err)
		}
		return resp.Result, resp.Error
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response")
		return nil, nil
	}
}

func TestSubmitRejected(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 1, mining.Rejected, submitter)
	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	client := newTestClient(server)
	req := submitRequest(1, []string{"miner", "1", "0102030405060708", "78563412", "ffeeddcc"})
	client.handleSubmit(req)

	if result, err := readResponse(t, client); err == nil || err.Code != ErrCodeLowDifficulty {
		t.Fatalf("expected low difficulty error, got result=%v err=%+v", result, err)
	}
	if submitter.count() != 0 {
		t.Fatal("submitter must not be called for rejected work")
	}
}

func TestSubmitAcceptedShare(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 1, mining.AcceptedShare, submitter)
	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	client := newTestClient(server)
	req := submitRequest(1, []string{"miner", "1", "0102030405060708", "78563412", "ffeeddcc"})
	client.handleSubmit(req)

	if result, err := readResponse(t, client); err != nil || result != true {
		t.Fatalf("expected accepted share, got result=%v err=%+v", result, err)
	}
	if submitter.count() != 0 {
		t.Fatal("submitter must not be called for a plain share")
	}
}

func TestSubmitBlockDivisorOne(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 1, mining.SubmitBlock, submitter)
	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	client := newTestClient(server)
	req := submitRequest(1, []string{"miner", "1", "0102030405060708", "78563412", "ffeeddcc"})
	client.handleSubmit(req)

	if result, err := readResponse(t, client); err != nil || result != true {
		t.Fatalf("expected accepted block, got result=%v err=%+v", result, err)
	}
	if submitter.count() != 1 {
		t.Fatalf("submitter called %d times, want 1", submitter.count())
	}

	// Verify the submission blob places each field at its canonical offset.
	submission := submitter.last()
	decoded, err := hex.DecodeString(submission)
	if err != nil {
		t.Fatalf("submission is not valid hex: %v", err)
	}
	if len(decoded) != mining.GetworkDataLen {
		t.Fatalf("submission length got %d want %d", len(decoded), mining.GetworkDataLen)
	}
	if !bytes.Equal(decoded[136:140], []byte{0x78, 0x56, 0x34, 0x12}) {
		t.Fatalf("timestamp bytes got %x", decoded[136:140])
	}
	if !bytes.Equal(decoded[140:144], []byte{0xff, 0xee, 0xdd, 0xcc}) {
		t.Fatalf("nonce bytes got %x", decoded[140:144])
	}
	if !bytes.Equal(decoded[144:148], []byte{0x01, 0x00, 0x00, 0x00}) {
		t.Fatalf("extraNonce1 bytes got %x", decoded[144:148])
	}
	if !bytes.Equal(decoded[148:156], []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}) {
		t.Fatalf("extraNonce2 bytes got %x", decoded[148:156])
	}
}

// TestSubmitBlockThrottle verifies that with a divisor of 2, only every second
// solved block is submitted to the node.
func TestSubmitBlockThrottle(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 2, mining.SubmitBlock, submitter)
	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	client := newTestClient(server)
	req := submitRequest(1, []string{"miner", "1", "0102030405060708", "78563412", "ffeeddcc"})

	// First found block must be throttled but accepted to the miner.
	client.handleSubmit(req)
	if result, err := readResponse(t, client); err != nil || result != true {
		t.Fatalf("throttled block: expected accepted result, got %v %+v", result, err)
	}
	if submitter.count() != 0 {
		t.Fatalf("first block must be throttled, submitter called %d times", submitter.count())
	}

	// Second found block must be submitted.
	client.handleSubmit(req)
	if result, err := readResponse(t, client); err != nil || result != true {
		t.Fatalf("second block: expected accepted result, got %v %+v", result, err)
	}
	if submitter.count() != 1 {
		t.Fatalf("second block must be submitted, got %d calls", submitter.count())
	}

	found, submitted, throttled := server.throttle.Stats()
	if found != 2 || submitted != 1 || throttled != 1 {
		t.Fatalf("throttle stats got found=%d submitted=%d throttled=%d",
			found, submitted, throttled)
	}
}

func TestSubmitBlockThrottleShareNotThrottled(t *testing.T) {
	// Shares (AcceptedShare) must never be throttled or counted as found
	// blocks even when a divisor is configured.
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 2, mining.AcceptedShare, submitter)
	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	client := newTestClient(server)
	req := submitRequest(1, []string{"miner", "1", "0102030405060708", "78563412", "ffeeddcc"})
	for i := 0; i < 5; i++ {
		client.handleSubmit(req)
		if result, err := readResponse(t, client); err != nil || result != true {
			t.Fatalf("share %d: expected accepted, got %v %+v", i, result, err)
		}
	}
	found, submitted, throttled := server.throttle.Stats()
	if found != 0 || submitted != 0 || throttled != 0 {
		t.Fatalf("shares must not touch throttle stats: got found=%d submitted=%d throttled=%d",
			found, submitted, throttled)
	}
	if submitter.count() != 0 {
		t.Fatal("submitter must not be called for shares")
	}
}

func TestSubmitUnknownJob(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 1, mining.SubmitBlock, submitter)
	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	client := newTestClient(server)
	req := submitRequest(1, []string{"miner", "999", "0102030405060708", "78563412", "ffeeddcc"})
	client.handleSubmit(req)

	if result, err := readResponse(t, client); err == nil || err.Code != ErrCodeStaleShare {
		t.Fatalf("expected stale share error, got result=%v err=%+v", result, err)
	}
	if submitter.count() != 0 {
		t.Fatal("submitter must not be called for an unknown job")
	}
}

func TestSubmitWorkerMismatch(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 1, mining.SubmitBlock, submitter)
	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	client := newTestClient(server)
	req := submitRequest(1, []string{"attacker", "1", "0102030405060708", "78563412", "ffeeddcc"})
	client.handleSubmit(req)

	if result, err := readResponse(t, client); err == nil || err.Code != ErrCodeUnauthorized {
		t.Fatalf("expected unauthorized error, got result=%v err=%+v", result, err)
	}
	if submitter.count() != 0 {
		t.Fatal("submitter must not be called for a mismatched worker")
	}
}

func TestSubmitInvalidHex(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 1, mining.SubmitBlock, submitter)
	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	client := newTestClient(server)
	req := submitRequest(1, []string{"miner", "1", "zz", "78563412", "ffeeddcc"})
	client.handleSubmit(req)

	if result, err := readResponse(t, client); err == nil || err.Code != ErrCodeUnknown {
		t.Fatalf("expected unknown error for bad hex, got result=%v err=%+v", result, err)
	}
	if submitter.count() != 0 {
		t.Fatal("submitter must not be called for invalid submission")
	}
}

func TestSubmitTooFewParams(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 1, mining.SubmitBlock, submitter)
	client := newTestClient(server)

	req := submitRequest(1, []string{"miner", "1"})
	client.handleSubmit(req)

	if result, err := readResponse(t, client); err == nil || err.Code != ErrCodeUnknown {
		t.Fatalf("expected unknown error for short params, got result=%v err=%+v", result, err)
	}
}

func TestSubscribe(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 1, mining.SubmitBlock, submitter)
	server.workMgr.SetCurrent(makeWorkBlob(t), mining.ReasonNewTxns)

	client := newTestClient(server)

	raw, _ := json.Marshal(map[string]interface{}{
		"id":     1,
		"method": Subscribe,
		"params": []string{"gominer/1.0"},
	})
	req, err := ParseRequest(raw)
	if err != nil {
		t.Fatalf("unable to parse subscribe: %v", err)
	}
	client.handleSubscribe(req)

	// First message is the subscribe result with extraNonce1 and length.
	msg1 := <-client.sends
	var resultMsg struct {
		Result []interface{} `json:"result"`
		Error  *StratumError `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(msg1), &resultMsg); err != nil {
		t.Fatalf("unable to parse subscribe result: %v", err)
	}
	if resultMsg.Error != nil {
		t.Fatalf("subscribe returned error: %+v", resultMsg.Error)
	}
	if len(resultMsg.Result) != 3 {
		t.Fatalf("subscribe result length got %d", len(resultMsg.Result))
	}
	extraNonce1, ok := resultMsg.Result[1].(string)
	if !ok || extraNonce1 != client.extraNonce1 {
		t.Fatalf("unexpected extraNonce1 %v", resultMsg.Result[1])
	}

	// Second message is the set_difficulty notification.
	msg2 := <-client.sends
	var diffMsg struct {
		Method string        `json:"method"`
		Params []interface{} `json:"params"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(msg2), &diffMsg); err != nil {
		t.Fatalf("unable to parse difficulty: %v", err)
	}
	if diffMsg.Method != SetDifficulty || len(diffMsg.Params) != 1 {
		t.Fatalf("unexpected difficulty message %+v", diffMsg)
	}

	// Third message is the work notification.
	msg3 := <-client.sends
	var workMsg struct {
		Method string        `json:"method"`
		Params []interface{} `json:"params"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(msg3), &workMsg); err != nil {
		t.Fatalf("unable to parse work notify: %v", err)
	}
	if workMsg.Method != Notify {
		t.Fatalf("expected notify, got %q", workMsg.Method)
	}
	if len(workMsg.Params) != 9 {
		t.Fatalf("notify params got %d want 9", len(workMsg.Params))
	}
}

func TestAuthorize(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := NewServer(context.Background(), &Config{
		Net:                chaincfg.MainNetParams(),
		ShareDifficulty:    100,
		BlockSubmitDivisor: 1,
		PoolPassword:       "secret",
		MaxClients:         10,
		Log:                testLogger(),
	}, submitter)

	client := newTestClient(server)

	// Wrong password must be rejected.
	bad := submitRequest(1, []string{"miner", "wrong"})
	client.handleAuthorize(bad)
	if result, err := readResponse(t, client); err == nil || err.Code != ErrCodeUnauthorized {
		t.Fatalf("expected unauthorized, got result=%v err=%+v", result, err)
	}
	if client.authorized {
		t.Fatal("client must not be authorized with wrong password")
	}

	// Correct password must authorize.
	good := submitRequest(2, []string{"miner", "secret"})
	client.handleAuthorize(good)
	if result, err := readResponse(t, client); err != nil || result != true {
		t.Fatalf("expected authorized, got result=%v err=%+v", result, err)
	}
	if !client.authorized {
		t.Fatal("client must be authorized with correct password")
	}
}

func TestAuthorizeNoPassword(t *testing.T) {
	submitter := &fakeSubmitter{accepted: true}
	server := newTestServer(t, 1, mining.SubmitBlock, submitter)
	client := newTestClient(server)

	req := submitRequest(1, []string{"anyworker", "anything"})
	client.handleAuthorize(req)
	if result, err := readResponse(t, client); err != nil || result != true {
		t.Fatalf("expected any worker authorized, got result=%v err=%+v", result, err)
	}
}
