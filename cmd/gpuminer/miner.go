package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/blockchain/standalone"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"lukechampine.com/blake3"
)

const (
	// userAgent is advertised in the subscribe request.
	userAgent = "monetarium-gpuminer/" + version

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

// Header field offsets within the 180 byte serialized block header.  These
// match the wire package and internal/mining/header.go.
const (
	headerLen           = 180
	versionOffset       = 0
	prevBlockOffset     = 4
	partialHeaderOffset = 36
	heightOffset        = 128
	timestampOffset     = 136
	nonceOffset         = 140
	extraNonce1Offset   = 144
	extraNonce2Offset   = 148

	// genTx1Len is the length of the partial header served in mining.notify.
	genTx1Len = headerLen - partialHeaderOffset
)

// MinerConfig configures the GPU miner.
type MinerConfig struct {
	Pool     string
	User     string
	Password string
	Net      *chaincfg.Params
	Host     string
	Kernels  string
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

// Job is a single unit of work to be mined.
type Job struct {
	jobID         string
	height        int64
	header        [headerLen]byte
	ntime         [4]byte
	shareTargetBE [32]byte
	blockTargetBE [32]byte
	solved        atomic.Bool
}

// buildJob reconstructs the serialized block header from the mining.notify
// fields.  The reconstruction mirrors the gominer PrepWork layout documented in
// internal/mining/notify_test.go: version, previous block hash and the partial
// header (genTx1) are concatenated and the miner controlled fields are placed
// at their canonical offsets.
func (m *Miner) buildJob(jobID, prevHashHex, genTx1Hex, versionHex, nbitsHex,
	ntimeHex string) (*Job, error) {

	version, err := hex.DecodeString(versionHex)
	if err != nil || len(version) != 4 {
		return nil, errors.New("invalid version field")
	}
	prevHash, err := hex.DecodeString(prevHashHex)
	if err != nil || len(prevHash) != 32 {
		return nil, errors.New("invalid prevhash field")
	}
	genTx1, err := hex.DecodeString(genTx1Hex)
	if err != nil || len(genTx1) != genTx1Len {
		return nil, errors.New("invalid genTx1 field")
	}
	nbits, err := hex.DecodeString(nbitsHex)
	if err != nil || len(nbits) != 4 {
		return nil, errors.New("invalid nbits field")
	}
	ntime, err := hex.DecodeString(ntimeHex)
	if err != nil || len(ntime) != 4 {
		return nil, errors.New("invalid ntime field")
	}
	extraNonce1, err := hex.DecodeString(m.extraNonce1)
	if err != nil || len(extraNonce1) != 4 {
		return nil, fmt.Errorf("invalid extraNonce1 %q", m.extraNonce1)
	}

	var header [headerLen]byte
	copy(header[versionOffset:], version)
	copy(header[prevBlockOffset:], prevHash)
	copy(header[partialHeaderOffset:], genTx1)
	copy(header[timestampOffset:], ntime)
	copy(header[extraNonce1Offset:], extraNonce1)

	job := &Job{
		jobID:         jobID,
		height:        int64(binary.LittleEndian.Uint32(header[heightOffset : heightOffset+4])),
		header:        header,
		shareTargetBE: toTargetBytes(m.shareTarget()),
		blockTargetBE: toTargetBytes(standalone.CompactToBig(binary.LittleEndian.Uint32(nbits))),
	}
	copy(job.ntime[:], ntime)
	return job, nil
}

// headerHex renders the job header with the given extraNonce2 value placed at
// its canonical offset.  The nonce field is left zero for the GPU to sweep.
func (j *Job) headerHex(extraNonce2 uint64) string {
	h := j.header
	binary.LittleEndian.PutUint64(h[extraNonce2Offset:extraNonce2Offset+8], extraNonce2)
	return hex.EncodeToString(h[:])
}

// shareTargetHex renders the share target as little endian bytes, the byte
// order expected by the GPU kernel target buffer.
func (j *Job) shareTargetHex() string {
	var le [32]byte
	for i, b := range j.shareTargetBE {
		le[31-i] = b
	}
	return hex.EncodeToString(le[:])
}

// toTargetBytes renders target as a 32 byte big-endian value, matching how the
// pool compares the little-endian blake3 output against the target.
func toTargetBytes(target *big.Int) [32]byte {
	var be [32]byte
	target.FillBytes(be[:])
	return be
}

// cmpHashTarget compares the little-endian blake3 proof of work sum against a
// target's big-endian representation and returns -1, 0 or +1 when the hash is
// less than, equal to or greater than the target.  It mirrors
// standalone.HashToBig(hash).Cmp(target) without allocating a big.Int per hash.
func cmpHashTarget(sum [32]byte, be *[32]byte) int {
	for i := 0; i < len(be); i++ {
		if a, b := sum[31-i], be[i]; a != b {
			if a < b {
				return -1
			}
			return +1
		}
	}
	return 0
}

// classifySolution decides whether a proof of work hash meets the share and/or
// block targets.
func classifySolution(sum [32]byte, shareBE, blockBE *[32]byte) (isShare, isBlock bool) {
	if cmpHashTarget(sum, shareBE) > 0 {
		return false, false
	}
	return true, cmpHashTarget(sum, blockBE) <= 0
}

// Miner is a stratum GPU miner.
type Miner struct {
	cfg    MinerConfig
	log    slog.Logger
	ctx    context.Context
	cancel context.CancelFunc

	connMu sync.Mutex
	conn   net.Conn
	reader *bufio.Reader

	idMu   sync.Mutex
	nextID uint64

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

	host *gpuHost
}

// NewMiner creates a GPU miner.
func NewMiner(ctx context.Context, cfg MinerConfig) *Miner {
	mctx, cancel := context.WithCancel(ctx)
	return &Miner{
		cfg:            cfg,
		log:            cfg.Log,
		ctx:            mctx,
		cancel:         cancel,
		extranonce2Len: expectedExtraNonce2Len,
	}
}

// Run connects to the pool and mines until the context is cancelled,
// reconnecting automatically when the connection is lost.
func (m *Miner) Run() error {
	host, err := startGpuHost(m.cfg.Host, m.cfg.Kernels, m.log)
	if err != nil {
		return fmt.Errorf("unable to start GPU host: %w", err)
	}
	m.host = host
	defer host.stop()
	m.log.Infof("GPU host started (pid %d)", host.cmd.Process.Pid)

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

	connCtx, connCancel := context.WithCancel(m.ctx)
	defer connCancel()
	closeConn := make(chan struct{})
	defer close(closeConn)
	go func() {
		select {
		case <-connCtx.Done():
			conn.Close()
		case <-closeConn:
		}
	}()

	m.gen.Add(1)
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

	var wg sync.WaitGroup
	readDone := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(readDone)
		m.readLoop()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		m.hashWorker(connCtx)
	}()

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
	_ = id
}

// hashWorker continuously mines the current job by sweeping the nonce space on
// the GPU and rolling extraNonce2 when a sweep completes without a solution.
func (m *Miner) hashWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job := m.currentJob.Load()
		if job == nil {
			m.waitForJob(ctx)
			continue
		}
		if job.solved.Load() {
			m.waitForJob(ctx)
			continue
		}
		gen := m.gen.Load()
		m.mineJob(ctx, job, gen)
	}
}

// waitForJob pauses until new work arrives or the connection is torn down.
func (m *Miner) waitForJob(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
	}
}

// mineJob drives the GPU host to search the current job.  The GPU searches the
// 2^32 nonce space for one header; when a sweep completes without a solution
// the extraNonce2 is rolled and the same header is re-submitted, giving a fresh
// disjoint nonce space.  It returns when the job generation changes (new work)
// or the job has been solved.
func (m *Miner) mineJob(ctx context.Context, job *Job, gen uint64) {
	extraNonce2 := uint64(0)
	var lastNonces uint64

	for {
		if ctx.Err() != nil || m.gen.Load() != gen || job.solved.Load() {
			return
		}

		if err := m.host.send(workMessage{
			Type:   "work",
			Header: job.headerHex(extraNonce2),
			Target: job.shareTargetHex(),
		}); err != nil {
			m.log.Errorf("GPU send failed: %v", err)
			return
		}
		lastNonces = 0

	select_loop:
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-m.host.progress:
				if p.NoncesChecked > lastNonces {
					m.hashes.Add(p.NoncesChecked - lastNonces)
					lastNonces = p.NoncesChecked
				}
			case s := <-m.host.searched:
				if s.NoncesChecked > lastNonces {
					m.hashes.Add(s.NoncesChecked - lastNonces)
				}
				break select_loop
			case sol := <-m.host.solutions:
				if sol.NoncesChecked > lastNonces {
					m.hashes.Add(sol.NoncesChecked - lastNonces)
				}
				extraNonce2B := make([]byte, 8)
				binary.LittleEndian.PutUint64(extraNonce2B, extraNonce2)
				m.onSolution(job, extraNonce2B, sol.Nonce)
				break select_loop
			}
		}

		// A solution or a complete sweep advances to the next extraNonce2.
		extraNonce2++
	}
}

// onSolution classifies a found solution and submits it if it meets the share
// target.
func (m *Miner) onSolution(job *Job, extraNonce2 []byte, nonce uint32) {
	// Rebuild the solved header to classify the hash against the targets.
	var header [headerLen]byte
	copy(header[:], job.header[:])
	binary.LittleEndian.PutUint64(header[extraNonce2Offset:extraNonce2Offset+8],
		binary.LittleEndian.Uint64(extraNonce2))
	binary.LittleEndian.PutUint32(header[nonceOffset:nonceOffset+4], nonce)
	sum := blake3.Sum256(header[:])

	if isShare, isBlock := classifySolution(sum, &job.shareTargetBE,
		&job.blockTargetBE); isShare {
		if isBlock {
			m.blocks.Add(1)
			m.gen.Add(1)
			job.solved.Store(true)
			m.log.Infof("block solution found: job=%s height=%d hash=%s",
				job.jobID, job.height, chainhash.Hash(sum))
		}
		m.submitShare(job, extraNonce2, nonce)
		if isBlock {
			return
		}
	}
}

// statsLoop logs periodic hashrate and share statistics.
func (m *Miner) statsLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	prevHashes := uint64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hashes := m.hashes.Load()
			rate := float64(hashes-prevHashes) / 5.0
			prevHashes = hashes
			m.log.Infof("hashrate=%s hashes=%d accepted=%d rejected=%d blocks=%d",
				formatHashrate(rate), hashes, m.accepted.Load(),
				m.rejected.Load(), m.blocks.Load())
		}
	}
}

// submitParams builds the mining.submit parameter list.
func submitParams(worker, jobID string, extraNonce2 []byte, ntime []byte,
	nonce []byte) []string {

	return []string{
		worker,
		jobID,
		hex.EncodeToString(extraNonce2),
		hex.EncodeToString(ntime),
		hex.EncodeToString(nonce),
	}
}

// formatHashrate renders a hashes-per-second value for logging.
func formatHashrate(h float64) string {
	switch {
	case h >= 1e9:
		return fmt.Sprintf("%.2f GH/s", h/1e9)
	case h >= 1e6:
		return fmt.Sprintf("%.2f MH/s", h/1e6)
	case h >= 1e3:
		return fmt.Sprintf("%.2f KH/s", h/1e3)
	default:
		return fmt.Sprintf("%.0f H/s", h)
	}
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

// putUint32LE writes v to b in little endian.
func putUint32LE(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
}
