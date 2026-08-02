package stratum

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/blockchain/standalone"

	"github.com/monetarium/monetarium-stratum/internal/mining"
)

// Client represents a connected miner.
type Client struct {
	conn              net.Conn
	server            *Server
	log               slog.Logger
	id                uint64
	extraNonce1       string
	extraNonce2Length int
	worker            string
	authorized        bool

	sends chan []byte
	quit  chan struct{}
	wg    sync.WaitGroup
	done  sync.Once

	submissions int64
}

// newClient creates a new client for the provided connection.
func newClient(server *Server, conn net.Conn) *Client {
	return &Client{
		conn:              conn,
		server:            server,
		log:               server.log,
		id:                server.nextClientID(),
		extraNonce1:       server.assignExtraNonce1(),
		extraNonce2Length: server.extraNonce2Length,
		sends:             make(chan []byte, 128),
		quit:              make(chan struct{}),
	}
}

// start launches the read and write loops for the client.
func (c *Client) start() {
	c.wg.Add(2)
	go c.readLoop()
	go c.writeLoop()
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	defer c.close()

	reader := bufio.NewReader(c.conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err.Error() != "EOF" {
				c.log.Debugf("client %d read error: %v", c.id, err)
			}
			return
		}
		c.handleMessage(line)
	}
}

func (c *Client) writeLoop() {
	defer c.wg.Done()
	for {
		select {
		case msg := <-c.sends:
			if _, err := c.conn.Write(msg); err != nil {
				c.log.Debugf("client %d write error: %v", c.id, err)
				return
			}
		case <-c.quit:
			return
		}
	}
}

// handleMessage dispatches an incoming request.
func (c *Client) handleMessage(raw []byte) {
	req, err := ParseRequest(raw)
	if err != nil {
		c.sendError(0, ErrCodeUnknown, "invalid request")
		return
	}

	switch req.Method {
	case Subscribe:
		c.handleSubscribe(req)
	case Authorize:
		c.handleAuthorize(req)
	case Submit:
		c.handleSubmit(req)
	case SuggestDiff:
		c.log.Tracef("client %d suggested difficulty, ignoring", c.id)
		c.sendResult(req.ID, true)
	default:
		c.sendError(req.ID, ErrCodeUnknown, fmt.Sprintf("unknown method %q", req.Method))
	}
}

func (c *Client) handleSubscribe(req *Request) {
	c.log.Debugf("client %d subscribed", c.id)

	// The result is the standard bitcoin stratum subscription reply:
	// [[[method, subscription-id], ...], extraNonce1, extraNonce2Length].
	result := []interface{}{
		[]interface{}{
			[]string{SetDifficulty, "1"},
			[]string{Notify, "1"},
		},
		c.extraNonce1,
		c.extraNonce2Length,
	}
	c.sendResult(req.ID, result)
	c.sendNotification(NewSetDifficulty(float64(c.server.shareDiff)))
	if work := c.server.workMgr.Current(); work != nil {
		c.sendWork(work, false)
	}
}

func (c *Client) handleAuthorize(req *Request) {
	params, err := req.ParseStringParams()
	if err != nil || len(params) < 2 {
		c.sendError(req.ID, ErrCodeUnauthorized, "invalid authorize params")
		return
	}
	user, pass := params[0], params[1]
	authorized := user != "" && c.server.authorize(user, pass)
	if !authorized {
		c.log.Warnf("client %d authorization failed for %q", c.id, user)
		c.sendError(req.ID, ErrCodeUnauthorized, "unauthorized worker")
		return
	}
	c.worker = user
	c.authorized = true
	c.log.Debugf("client %d authorized as %q", c.id, user)
	c.sendResult(req.ID, true)
}

// handleSubmit processes a mining.submit request, validating the work and
// submitting solved blocks to the node subject to the block throttle.
func (c *Client) handleSubmit(req *Request) {
	params, err := req.ParseStringParams()
	if err != nil || len(params) < 5 {
		c.sendError(req.ID, ErrCodeUnknown, "invalid submit params")
		return
	}
	worker, jobID, extraNonce2E, nTimeE, nonceE :=
		params[0], params[1], params[2], params[3], params[4]

	if worker != c.worker {
		c.sendError(req.ID, ErrCodeUnauthorized, "worker name mismatch")
		return
	}

	job, ok := c.server.workMgr.Job(jobID)
	if !ok {
		c.log.Debugf("client %d submitted unknown job %q", c.id, jobID)
		c.sendError(req.ID, ErrCodeStaleShare, "job not found")
		return
	}

	extraNonce2, err := hex.DecodeString(extraNonce2E)
	if err != nil {
		c.sendError(req.ID, ErrCodeUnknown, "invalid extraNonce2")
		return
	}
	nTime, err := hex.DecodeString(nTimeE)
	if err != nil {
		c.sendError(req.ID, ErrCodeUnknown, "invalid timestamp")
		return
	}
	nonce, err := hex.DecodeString(nonceE)
	if err != nil {
		c.sendError(req.ID, ErrCodeUnknown, "invalid nonce")
		return
	}
	extraNonce1, err := hex.DecodeString(c.extraNonce1)
	if err != nil {
		c.sendError(req.ID, ErrCodeUnknown, "invalid extraNonce1")
		return
	}

	header, err := job.BuildSolvedHeader(extraNonce1, extraNonce2, nTime, nonce)
	if err != nil {
		c.log.Debugf("client %d unable to build solved header: %v", c.id, err)
		c.sendError(req.ID, ErrCodeUnknown, "invalid submission")
		return
	}

	powHash := header.PowHashV2()
	hashTarget := standalone.HashToBig(&powHash)
	shareTarget := c.server.shareTarget()
	blockTarget := standalone.CompactToBig(header.Bits)

	switch c.server.evaluate(hashTarget, shareTarget, blockTarget) {
	case mining.Rejected:
		c.log.Debugf("client %d submitted low difficulty share %v", c.id, powHash)
		c.sendError(req.ID, ErrCodeLowDifficulty, "low difficulty share")
		return
	case mining.AcceptedShare:
		// It is merely a share; accept it without network submission.
		c.server.recordShare(c)
		c.log.Debugf("client %d share accepted %v", c.id, powHash)
		c.sendResult(req.ID, true)
		return
	case mining.SubmitBlock:
	}

	atomic.AddInt64(&c.submissions, 1)

	// Found a block.  The throttle may discard it to keep the pool from
	// dominating the network.
	if !c.server.throttle.Allow() {
		c.server.recordShare(c)
		found, submitted, throttled := c.server.throttle.Stats()
		c.log.Infof("block found but throttled: skipped submission (%d found, "+
			"%d submitted, %d throttled)", found, submitted, throttled)
		c.sendResult(req.ID, true)
		return
	}

	submission, err := job.SolvedHeaderData(extraNonce1, extraNonce2, nTime, nonce)
	if err != nil {
		c.sendError(req.ID, ErrCodeUnknown, "unable to serialize submission")
		return
	}
	accepted, err := c.server.submitter.SubmitWork(submission)
	if err != nil {
		// If the block difficulty changed, request new work.
		if strings.Contains(err.Error(), "block difficulty of") {
			c.server.requestNewWork()
		}
		c.log.Errorf("client %d work submission failed: %v", c.id, err)
		c.sendError(req.ID, ErrCodeUnknown, "work submission failed")
		return
	}
	if !accepted {
		c.log.Debugf("client %d work rejected by the node %v", c.id, powHash)
		c.sendError(req.ID, ErrCodeUnknown, "work rejected by the node")
		return
	}

	c.log.Infof("block %v submitted and accepted at height %d by %q",
		header.BlockHash(), job.Height(), c.worker)
	c.sendResult(req.ID, true)
}

// sendWork sends a work notification to the client.
func (c *Client) sendWork(work *mining.Work, cleanJobs bool) {
	ntfn := NewNotify(
		work.JobID(),
		hex.EncodeToString(work.PrevBlock()),
		hex.EncodeToString(work.PartialHeader()),
		"",
		[]string{},
		hex.EncodeToString(work.Version()),
		hex.EncodeToString(work.Bits()),
		hex.EncodeToString(work.Timestamp()),
		cleanJobs,
	)
	c.sendNotification(ntfn)
}

func (c *Client) sendResult(id uint64, result interface{}) {
	c.enqueue(NewResponse(id, result))
}

func (c *Client) sendError(id uint64, code int, message string) {
	c.enqueue(NewErrorResponse(id, code, message))
}

func (c *Client) sendNotification(ntfn *Notification) {
	c.enqueue(ntfn)
}

func (c *Client) enqueue(msg interface{}) {
	raw, err := marshalMessage(msg)
	if err != nil {
		c.log.Errorf("client %d unable to marshal message: %v", c.id, err)
		return
	}
	select {
	case c.sends <- raw:
	case <-c.quit:
	}
}

// close terminates the client connection.
func (c *Client) close() {
	c.done.Do(func() {
		close(c.quit)
		c.conn.Close()
		c.server.removeClient(c)
		c.log.Debugf("client %d disconnected", c.id)
	})
}

// wait blocks until the client read and write loops have exited.
func (c *Client) wait() {
	c.wg.Wait()
}
