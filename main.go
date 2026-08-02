package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/chaincfg"

	"github.com/monetarium/monetarium-stratum/internal/node"
	"github.com/monetarium/monetarium-stratum/internal/stratum"
)

const (
	// version is the application version.
	version = "0.1.0"

	// workEventBuffer bounds the number of queued work events from the node.
	workEventBuffer = 8
)

func main() {
	// Parse the configuration.
	cfg, err := parseConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Set up logging.
	logger := setupLogger(cfg.DebugLevel)
	logger.Infof("monetarium-stratum %s starting", version)

	// Create the root context cancelled on shutdown signal.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the node client.
	workCh := make(chan *node.WorkEvent, workEventBuffer)
	blockCh := make(chan struct{}, 1)
	nodeClient, err := node.New(ctx, &node.Config{
		Host:     cfg.NodeRPC,
		User:     cfg.RPCUser,
		Pass:     cfg.RPCPass,
		CertFile: cfg.RPCCert,
		Log:      logger,
	}, workCh, blockCh)
	if err != nil {
		logger.Errorf("unable to create node client: %v", err)
		os.Exit(1)
	}

	// Create the stratum server.
	server := stratum.NewServer(ctx, &stratum.Config{
		Net:                chaincfg.MainNetParams(),
		ShareDifficulty:    cfg.ShareDifficulty,
		BlockSubmitDivisor: cfg.BlockSubmitDivisor,
		PoolPassword:       cfg.PoolPassword,
		MaxClients:         cfg.MaxClients,
		Log:                logger,
	}, nodeClient)

	// Start the stratum server.
	if err := server.Start(cfg.Listen); err != nil {
		logger.Errorf("unable to start stratum server: %v", err)
		os.Exit(1)
	}

	// Connect to the node and fetch the initial work.
	if err := nodeClient.Connect(); err != nil {
		logger.Errorf("unable to connect to node: %v", err)
		os.Exit(1)
	}
	logger.Infof("connected to node at %s", cfg.NodeRPC)

	// Run the main event loop.
	run(ctx, logger, server, nodeClient, workCh, blockCh, cfg)

	// Shutdown.
	server.Stop()
	nodeClient.Shutdown()
	logger.Info("monetarium-stratum shutdown complete")
}

// run executes the main event loop, dispatching node events and handling
// shutdown.
func run(ctx context.Context, log slog.Logger, server *stratum.Server,
	nodeClient *node.Client, workCh chan *node.WorkEvent, blockCh chan struct{},
	cfg *config) {

	statsTicker := time.NewTicker(5 * time.Minute)
	defer statsTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case work := <-workCh:
			server.ProcessWork(work)
		case <-blockCh:
			server.NewBlock()
		case <-statsTicker.C:
			logBlockStats(log, server)
		case <-sigCh:
			log.Info("shutdown signal received")
			return
		case <-ctx.Done():
			return
		}
	}
}

// logBlockStats logs the current block throttle and share statistics.
func logBlockStats(log slog.Logger, server *stratum.Server) {
	found, submitted, throttled := server.BlockStats()
	log.Infof("stats: shares=%d blocks found=%d submitted=%d throttled=%d",
		server.ShareCount(), found, submitted, throttled)
}
