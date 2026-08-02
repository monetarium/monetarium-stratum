package node

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/rpcclient"

	"github.com/monetarium/monetarium-stratum/internal/mining"
)

// Config configures the connection to the monetarium-node RPC server.
type Config struct {
	// Host is the node RPC server host and port.
	Host string

	// User is the node RPC username.
	User string

	// Pass is the node RPC password.
	Pass string

	// CertFile is the path to the node RPC TLS certificate.  When empty, TLS
	// is disabled.
	CertFile string

	// Log is the package logger.
	Log slog.Logger
}

// Client manages the connection to the monetarium-node RPC server and the
// delivery of new work and block notifications to the pool.
type Client struct {
	cfg       *Config
	client    *rpcclient.Client
	log       slog.Logger
	work      chan *WorkEvent
	blocks    chan struct{}
	connected chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
}

// WorkEvent describes newly available work received from the node.
type WorkEvent struct {
	// Data is the raw getwork data blob (192 bytes).
	Data []byte

	// Reason is one of the mining.Reason* constants.
	Reason string
}

// New creates a node client.  Work notifications are delivered on the returned
// channel; block connected notifications on the block channel.
func New(ctx context.Context, cfg *Config, work chan *WorkEvent, blocks chan struct{}) (*Client, error) {
	cctx, cancel := context.WithCancel(ctx)
	c := &Client{
		cfg:       cfg,
		log:       cfg.Log,
		work:      work,
		blocks:    blocks,
		connected: make(chan struct{}, 1),
		ctx:       cctx,
		cancel:    cancel,
	}

	connCfg := &rpcclient.ConnConfig{
		Host:                 cfg.Host,
		Endpoint:             "ws",
		User:                 cfg.User,
		Pass:                 cfg.Pass,
		DisableTLS:           cfg.CertFile == "",
		DisableAutoReconnect: false,
	}
	if cfg.CertFile != "" {
		certs, err := os.ReadFile(cfg.CertFile)
		if err != nil {
			return nil, fmt.Errorf("unable to read rpc certificate: %w", err)
		}
		connCfg.Certificates = certs
	}

	handlers := &rpcclient.NotificationHandlers{
		OnClientConnected: func() {
			select {
			case c.connected <- struct{}{}:
			default:
			}
		},
		OnWork: func(data []byte, target []byte, reason string) {
			c.handleWork(data, reason)
		},
		OnBlockConnected: func(blockHeader []byte, transactions [][]byte) {
			c.handleBlockConnected()
		},
	}

	client, err := rpcclient.New(connCfg, handlers)
	if err != nil {
		return nil, fmt.Errorf("unable to create node client: %w", err)
	}
	c.client = client
	return c, nil
}

// Connect waits for the connection to be established, registers for work
// notifications and fetches the initial work so the pool can serve miners
// immediately.
func (c *Client) Connect() error {
	// Wait for the initial connection to be established.
	select {
	case <-c.connected:
	case <-c.ctx.Done():
		return c.ctx.Err()
	}

	if err := c.client.NotifyWork(c.ctx); err != nil {
		return fmt.Errorf("unable to register for work notifications: %w", err)
	}

	// Fetch initial work to avoid waiting for a notification.
	work, _, err := c.GetWork()
	if err != nil {
		return fmt.Errorf("unable to fetch initial work: %w", err)
	}
	c.handleWork(work, mining.ReasonNewTxns)
	return nil
}

// GetWork fetches the current work directly from the node.
func (c *Client) GetWork() ([]byte, []byte, error) {
	result, err := c.client.GetWork(c.ctx)
	if err != nil {
		return nil, nil, err
	}
	data, err := hex.DecodeString(result.Data)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to decode getwork data: %w", err)
	}
	target, err := hex.DecodeString(result.Target)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to decode getwork target: %w", err)
	}
	return data, target, nil
}

// SubmitWork submits a solved block to the node and reports whether it was
// accepted.  data is the hex encoded 192-byte getwork submission blob.
func (c *Client) SubmitWork(data string) (bool, error) {
	return c.client.GetWorkSubmit(c.ctx, data)
}

// Shutdown disconnects from the node.
func (c *Client) Shutdown() {
	c.once.Do(func() {
		c.cancel()
		if c.client != nil {
			c.client.Shutdown()
		}
	})
}

func (c *Client) handleWork(data []byte, reason string) {
	event := &WorkEvent{Data: data, Reason: reason}
	select {
	case c.work <- event:
	case <-c.ctx.Done():
	}
}

func (c *Client) handleBlockConnected() {
	select {
	case c.blocks <- struct{}{}:
	case <-c.ctx.Done():
	}
}
