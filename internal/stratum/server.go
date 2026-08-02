package stratum

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"net"
	"sync"
	"sync/atomic"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/chaincfg"

	"github.com/monetarium/monetarium-stratum/internal/mining"
	"github.com/monetarium/monetarium-stratum/internal/node"
)

// Submitter submits solved blocks to the mining node.
type Submitter interface {
	SubmitWork(data string) (bool, error)
}

// Evaluator decides how solved work should be handled.  It is a field to allow
// deterministic testing of the submit path.
type Evaluator func(hashTarget, shareTarget, blockTarget *big.Int) mining.Decision

// Config configures the stratum server.
type Config struct {
	// Net is the active network parameters.
	Net *chaincfg.Params

	// ShareDifficulty is the difficulty at which pool shares are accepted.
	ShareDifficulty uint32

	// BlockSubmitDivisor self-limits block submissions: only 1 in every N
	// solved blocks is submitted to the node (N = divisor).  A value of 1
	// disables throttling.
	BlockSubmitDivisor uint32

	// PoolPassword, when non-empty, is required to authorize workers.  When
	// empty any worker name is accepted.
	PoolPassword string

	// MaxClients bounds the number of concurrently connected miners.
	MaxClients int

	// Log is the package logger.
	Log slog.Logger
}

// Server is a stratum mining pool server.
type Server struct {
	cfg               *Config
	log               slog.Logger
	workMgr           *mining.WorkManager
	throttle          *mining.BlockThrottle
	submitter         Submitter
	params            *chaincfg.Params
	shareDiff         uint32
	extraNonce2Length int

	ln     net.Listener
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	clientsMu sync.RWMutex
	clients   map[uint64]*Client

	clientID uint64
	nonce1   uint32

	shareCount     uint64
	blockCount     uint64
	throttledCount uint64

	evaluate Evaluator
}

// NewServer creates a new stratum server.
func NewServer(ctx context.Context, cfg *Config, submitter Submitter) *Server {
	sctx, cancel := context.WithCancel(ctx)
	return &Server{
		cfg:               cfg,
		log:               cfg.Log,
		workMgr:           mining.NewWorkManager(1000),
		throttle:          mining.NewBlockThrottle(cfg.BlockSubmitDivisor),
		submitter:         submitter,
		params:            cfg.Net,
		shareDiff:         cfg.ShareDifficulty,
		extraNonce2Length: 8,
		ctx:               sctx,
		cancel:            cancel,
		clients:           make(map[uint64]*Client),
		evaluate:          mining.EvaluateSubmit,
	}
}

// Start begins accepting miner connections on the provided address.
func (s *Server) Start(listenAddr string) error {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.log.Infof("stratum server listening on %s", listenAddr)
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Stop shuts the server down.
func (s *Server) Stop() {
	s.cancel()
	if s.ln != nil {
		s.ln.Close()
	}
	s.clientsMu.Lock()
	clients := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.clientsMu.Unlock()
	for _, c := range clients {
		c.close()
	}
	s.wg.Wait()
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			s.log.Errorf("accept error: %v", err)
			continue
		}

		if s.clientCount() >= s.cfg.MaxClients {
			s.log.Warnf("max clients reached, rejecting %s", conn.RemoteAddr())
			conn.Close()
			continue
		}

		client := newClient(s, conn)
		s.addClient(client)
		client.start()
		s.log.Debugf("client %d connected from %s", client.id, conn.RemoteAddr())
	}
}

// BroadcastWork sends the current work to all connected clients.
func (s *Server) BroadcastWork() {
	work := s.workMgr.Current()
	if work == nil {
		return
	}
	s.clientsMu.RLock()
	clients := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.clientsMu.RUnlock()
	for _, c := range clients {
		c.sendWork(work, false)
	}
}

// ProcessWork receives new work from the node and broadcasts it.
func (s *Server) ProcessWork(work *node.WorkEvent) {
	curr, clean := s.workMgr.SetCurrent(work.Data, work.Reason)
	if curr == nil {
		return
	}
	s.clientsMu.RLock()
	clients := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.clientsMu.RUnlock()
	for _, c := range clients {
		c.sendWork(curr, clean)
	}
}

// NewBlock is invoked when a block is connected to the chain.  New work will
// follow via the work notification.
func (s *Server) NewBlock() {
	s.log.Debug("new block connected to the chain, awaiting new work")
}

// BlockStats returns the current throttle counters.
func (s *Server) BlockStats() (found uint64, submitted uint64, throttled uint64) {
	return s.throttle.Stats()
}

// ShareCount returns the number of accepted pool shares.
func (s *Server) ShareCount() uint64 {
	return atomic.LoadUint64(&s.shareCount)
}

func (s *Server) shareTarget() *big.Int {
	return mining.ShareTarget(s.params, s.shareDiff)
}

func (s *Server) authorize(user, pass string) bool {
	if s.cfg.PoolPassword == "" {
		return true
	}
	return pass == s.cfg.PoolPassword
}

func (s *Server) recordShare(c *Client) {
	atomic.AddUint64(&s.shareCount, 1)
}

func (s *Server) requestNewWork() {
	// Work will be refreshed by the node work notification on the next
	// template update.  Nothing to do for the solo pool beyond logging.
	s.log.Debug("requesting new work after block difficulty mismatch")
}

func (s *Server) nextClientID() uint64 {
	return atomic.AddUint64(&s.clientID, 1)
}

// assignExtraNonce1 returns a unique 4 byte extra nonce for a client.
func (s *Server) assignExtraNonce1() string {
	s.clientsMu.Lock()
	n := atomic.AddUint32(&s.nonce1, 1)
	s.clientsMu.Unlock()
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], n)
	return hex.EncodeToString(b[:])
}

func (s *Server) addClient(c *Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.clients[c.id] = c
}

func (s *Server) removeClient(c *Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	delete(s.clients, c.id)
}

func (s *Server) clientCount() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

// marshalMessage serializes a message with a trailing newline.
func marshalMessage(msg interface{}) ([]byte, error) {
	raw, err := jsonMarshal(msg)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
