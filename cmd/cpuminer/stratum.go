package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/chaincfg"
)

const (
	// userAgent is advertised in the subscribe request.
	userAgent = "monetarium-cpuminer/" + version

	// Stratum request methods.
	methodSubscribe = "mining.subscribe"
	methodAuthorize = "mining.authorize"
	methodSubmit    = "mining.submit"

	// Stratum server notification methods.
	ntfnDifficulty = "mining.set_difficulty"
	ntfnNotify     = "mining.notify"

	// notifyParams is the number of parameters in a mining.notify message.
	notifyParams = 9

	// expectedExtraNonce2Len is the extraNonce2 length served by this pool.
	expectedExtraNonce2Len = 8
)

// MinerConfig configures the CPU miner.
type MinerConfig struct {
	Pool     string
	User     string
	Password string
	Net      *chaincfg.Params
	Threads  int
	Log      slog.Logger
}

// message is a JSON-RPC message received from the server.  Responses carry an
// id and no method; notifications carry a method and an id of 0.
type message struct {
	ID     uint64            `json:"id"`
	Method string            `json:"method"`
	Result json.RawMessage   `json:"result"`
	Error  *json.RawMessage  `json:"error"`
	Params []json.RawMessage `json:"params"`
}

// Miner is a CPU stratum miner that behaves like a simulated ASIC device.
type Miner struct {
	cfg    MinerConfig
	log    slog.Logger
	ctx    context.Context
	cancel context.CancelFunc

	connMu sync.Mutex
	conn   net.Conn
	reader *bufio.Reader

	idMu      sync.Mutex
	nextID    uint64
	pendingMu sync.Mutex
	pending   map[uint64]struct{}

	extraNonce1    string
	extranonce2Len int

	diffMu sync.Mutex
	target *big.Int // share target derived from set_difficulty

	currentJob atomic.Pointer[Job]
	gen        atomic.Uint64

	hashes   atomic.Uint64
	accepted atomic.Uint64
	rejected atomic.Uint64
	blocks   atomic.Uint64

	// onSubmit is the share submission hook.  It is a field to allow tests to
	// capture submissions without a network connection.
	onSubmit func(job *Job, extraNonce2 []byte, nonce uint32)
}

// NewMiner creates a CPU miner.
func NewMiner(ctx context.Context, cfg MinerConfig) *Miner {
	mctx, cancel := context.WithCancel(ctx)
	m := &Miner{
		cfg:            cfg,
		log:            cfg.Log,
		ctx:            mctx,
		cancel:         cancel,
		pending:        make(map[uint64]struct{}),
		extranonce2Len: expectedExtraNonce2Len,
	}
	m.onSubmit = m.submitShare
	return m
}

// Run connects to the pool and mines until the context is cancelled, reconnecting
// automatically when the connection is lost.
func (m *Miner) Run() error {
	for {
		err := m.connectAndMine()
		if err == nil {
			return nil
		}
		select {
		case <-m.ctx.Done():
			return nil
		default:
		}
		m.log.Errorf("%v; reconnecting in 5s", err)
		select {
		case <-time.After(5 * time.Second):
		case <-m.ctx.Done():
			return nil
		}
	}
}

// connectAndMine establishes a connection, performs the handshake and mines
// until the connection is lost or the context is cancelled.
func (m *Miner) connectAndMine() error {
	conn, err := net.DialTimeout("tcp", m.cfg.Pool, 10*time.Second)
	if err != nil {
		return err
	}
	m.connMu.Lock()
	m.conn = conn
	m.reader = bufio.NewReader(conn)
	m.connMu.Unlock()
	m.log.Infof("connected to %s", m.cfg.Pool)

	// Reset per-connection state.
	m.pendingMu.Lock()
	m.pending = make(map[uint64]struct{})
	m.pendingMu.Unlock()
	m.diffMu.Lock()
	m.target = new(big.Int).Set(m.cfg.Net.PowLimit)
	m.diffMu.Unlock()

	if err := m.subscribe(); err != nil {
		conn.Close()
		return err
	}
	if err := m.authorize(); err != nil {
		conn.Close()
		return err
	}
	m.log.Infof("authorized as %q", m.cfg.User)

	connCtx, connCancel := context.WithCancel(m.ctx)
	defer connCancel()

	var wg sync.WaitGroup
	readDone := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(readDone)
		m.readLoop()
	}()

	for i := 0; i < m.cfg.Threads; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m.hashWorker(connCtx, idx)
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		m.statsLoop(connCtx)
	}()

	select {
	case <-readDone:
	case <-m.ctx.Done():
	}
	connCancel()
	wg.Wait()
	conn.Close()

	select {
	case <-m.ctx.Done():
		return nil
	default:
		return errors.New("connection lost")
	}
}

// readLoop reads messages until the connection drops.
func (m *Miner) readLoop() {
	for {
		msg, err := m.readMessage()
		if err != nil {
			select {
			case <-m.ctx.Done():
				return
			default:
				m.log.Debugf("read error: %v", err)
				return
			}
		}
		if msg.Method != "" {
			m.handleNotification(msg)
		} else {
			m.handleResponse(msg)
		}
	}
}

// readMessage reads and parses a single line-delimited JSON message.
func (m *Miner) readMessage() (*message, error) {
	m.connMu.Lock()
	reader := m.reader
	m.connMu.Unlock()
	if reader == nil {
		return nil, errors.New("not connected")
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var msg message
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, fmt.Errorf("invalid message %q: %w", string(line), err)
	}
	return &msg, nil
}

// sendRequest sends a request and returns its id.
func (m *Miner) sendRequest(method string, params interface{}) (uint64, error) {
	id := m.nextRequestID()
	raw, err := json.Marshal(map[string]interface{}{
		"id":     id,
		"method": method,
		"params": params,
	})
	if err != nil {
		return 0, err
	}
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if m.conn == nil {
		return 0, errors.New("not connected")
	}
	if _, err := m.conn.Write(append(raw, '\n')); err != nil {
		return 0, err
	}
	return id, nil
}

// nextRequestID returns the next request identifier.
func (m *Miner) nextRequestID() uint64 {
	m.idMu.Lock()
	defer m.idMu.Unlock()
	m.nextID++
	return m.nextID
}

// subscribe performs the mining.subscribe handshake and records the assigned
// extraNonce1 and extraNonce2 length.
func (m *Miner) subscribe() error {
	id, err := m.sendRequest(methodSubscribe, []string{userAgent})
	if err != nil {
		return err
	}
	for {
		msg, err := m.readMessage()
		if err != nil {
			return err
		}
		if msg.Method == "" && msg.ID == id {
			if msg.Error != nil {
				return fmt.Errorf("subscribe rejected: %s", jsonErrorString(msg.Error))
			}
			var result []interface{}
			if err := json.Unmarshal(msg.Result, &result); err != nil {
				return fmt.Errorf("invalid subscribe result: %w", err)
			}
			if len(result) != 3 {
				return fmt.Errorf("unexpected subscribe result length %d", len(result))
			}
			extraNonce1, _ := result[1].(string)
			extraNonce2Len, _ := result[2].(float64)
			m.extraNonce1 = extraNonce1
			m.extranonce2Len = int(extraNonce2Len)
			if m.extranonce2Len != expectedExtraNonce2Len {
				m.log.Warnf("server wants extraNonce2 length %d, expected %d",
					m.extranonce2Len, expectedExtraNonce2Len)
			}
			m.log.Debugf("subscribed, extraNonce1=%s extraNonce2Len=%d",
				extraNonce1, m.extranonce2Len)
			return nil
		}
		m.handleNotification(msg)
	}
}

// authorize performs the mining.authorize handshake.
func (m *Miner) authorize() error {
	id, err := m.sendRequest(methodAuthorize, []string{m.cfg.User, m.cfg.Password})
	if err != nil {
		return err
	}
	for {
		msg, err := m.readMessage()
		if err != nil {
			return err
		}
		if msg.Method == "" && msg.ID == id {
			if msg.Error != nil {
				return fmt.Errorf("authorize rejected: %s", jsonErrorString(msg.Error))
			}
			var ok bool
			if err := json.Unmarshal(msg.Result, &ok); err != nil {
				return fmt.Errorf("invalid authorize result: %w", err)
			}
			if !ok {
				return errors.New("authorize rejected by server")
			}
			return nil
		}
		m.handleNotification(msg)
	}
}

// handleNotification dispatches a server notification.
func (m *Miner) handleNotification(msg *message) {
	switch msg.Method {
	case ntfnDifficulty:
		var diff float64
		if len(msg.Params) > 0 {
			_ = json.Unmarshal(msg.Params[0], &diff)
		}
		m.setDifficulty(diff)
	case ntfnNotify:
		m.handleNotify(msg.Params)
	default:
		m.log.Debugf("ignoring notification %q", msg.Method)
	}
}

// handleResponse resolves a response to a share submission.
func (m *Miner) handleResponse(msg *message) {
	m.pendingMu.Lock()
	_, ok := m.pending[msg.ID]
	delete(m.pending, msg.ID)
	m.pendingMu.Unlock()
	if !ok {
		m.log.Debugf("response for unexpected request %d", msg.ID)
		return
	}
	if msg.Error != nil {
		m.rejected.Add(1)
		m.log.Warnf("share rejected: %s", jsonErrorString(msg.Error))
		return
	}
	m.accepted.Add(1)
	m.log.Debugf("share accepted")
}

// setDifficulty stores the share target derived from the difficulty.
func (m *Miner) setDifficulty(diff float64) {
	target := new(big.Int).Div(new(big.Int).Set(m.cfg.Net.PowLimit),
		new(big.Int).SetUint64(uint64(diff)))
	if target.Cmp(m.cfg.Net.PowLimit) > 0 {
		target.Set(m.cfg.Net.PowLimit)
	}
	m.diffMu.Lock()
	m.target = target
	m.diffMu.Unlock()
	m.log.Infof("difficulty set to %v", diff)
}

// shareTarget returns a copy of the current share target.
func (m *Miner) shareTarget() *big.Int {
	m.diffMu.Lock()
	defer m.diffMu.Unlock()
	if m.target == nil {
		return new(big.Int).Set(m.cfg.Net.PowLimit)
	}
	return new(big.Int).Set(m.target)
}

// handleNotify parses a mining.notify notification and installs the new job.
func (m *Miner) handleNotify(params []json.RawMessage) {
	if len(params) != notifyParams {
		m.log.Errorf("mining.notify with %d params, want %d", len(params), notifyParams)
		return
	}
	jobID := jsonParamString(params[0])
	prevHash := jsonParamString(params[1])
	genTx1 := jsonParamString(params[2])
	genTx2 := jsonParamString(params[3])
	var branches []string
	_ = json.Unmarshal(params[4], &branches)
	version := jsonParamString(params[5])
	nbits := jsonParamString(params[6])
	ntime := jsonParamString(params[7])
	var clean bool
	_ = json.Unmarshal(params[8], &clean)

	if genTx2 != "" || len(branches) > 0 {
		m.log.Warnf("job %s carries genTx2/merkle branches; this pool serves solo "+
			"templates so the merkle root is already in the header", jobID)
	}

	job, err := m.buildJob(jobID, prevHash, genTx1, version, nbits, ntime)
	if err != nil {
		m.log.Errorf("unable to build job %s: %v", jobID, err)
		return
	}

	m.gen.Add(1)
	m.currentJob.Store(job)

	if clean {
		m.log.Debugf("job %s marked clean, previous jobs are stale", jobID)
	}
	m.log.Infof("new job %s at height %d", jobID, job.height)
}

// registerPending records an in-flight submit request.
func (m *Miner) registerPending(id uint64) {
	m.pendingMu.Lock()
	m.pending[id] = struct{}{}
	m.pendingMu.Unlock()
}

// submitShare submits a found solution to the pool.
func (m *Miner) submitShare(job *Job, extraNonce2 []byte, nonce uint32) {
	var nonceB [4]byte
	putUint32LE(nonceB[:], nonce)
	params := submitParams(m.cfg.User, job.jobID, extraNonce2, job.ntime[:], nonceB[:])
	id, err := m.sendRequest(methodSubmit, params)
	if err != nil {
		m.log.Errorf("submit failed: %v", err)
		m.rejected.Add(1)
		return
	}
	m.registerPending(id)
}

// jsonParamString extracts a string from a JSON raw parameter.
func jsonParamString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// jsonErrorString renders a JSON-RPC error object.
func jsonErrorString(raw *json.RawMessage) string {
	if raw == nil {
		return "unknown error"
	}
	var e struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(*raw, &e); err != nil {
		return string(*raw)
	}
	if e.Message == "" {
		return fmt.Sprintf("code %d", e.Code)
	}
	return fmt.Sprintf("code %d: %s", e.Code, e.Message)
}
