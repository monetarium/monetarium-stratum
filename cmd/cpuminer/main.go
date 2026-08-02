package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/decred/slog"
	"github.com/monetarium/monetarium-node/chaincfg"
)

const version = "0.1.0"

// config holds the CPU miner configuration.
type config struct {
	pool     string
	user     string
	password string
	net      string
	threads  int
	debug    bool
}

// parseConfig parses the command line flags.
func parseConfig() *config {
	cfg := &config{}
	flag.StringVar(&cfg.pool, "pool", "127.0.0.1:5550", "stratum server host:port")
	flag.StringVar(&cfg.user, "user", "", "worker name")
	flag.StringVar(&cfg.password, "password", "", "worker password")
	flag.StringVar(&cfg.net, "net", "mainnet",
		"network (mainnet, testnet3, simnet, regnet) used to derive the share target")
	flag.IntVar(&cfg.threads, "threads", 1, "number of hashing threads")
	flag.BoolVar(&cfg.debug, "debug", false, "enable debug logging")
	flag.Parse()
	return cfg
}

// netParams returns the chain parameters for the named network.
func netParams(name string) (*chaincfg.Params, error) {
	switch strings.ToLower(name) {
	case "mainnet":
		return chaincfg.MainNetParams(), nil
	case "testnet3", "testnet":
		return chaincfg.TestNet3Params(), nil
	case "simnet":
		return chaincfg.SimNetParams(), nil
	case "regnet", "regtest":
		return chaincfg.RegNetParams(), nil
	}
	return nil, fmt.Errorf("unknown network %q", name)
}

func main() {
	cfg := parseConfig()

	if cfg.user == "" {
		fmt.Fprintln(os.Stderr, "the --user flag is required")
		os.Exit(1)
	}

	params, err := netParams(cfg.net)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	level := slog.LevelInfo
	if cfg.debug {
		level = slog.LevelDebug
	}
	backend := slog.NewBackend(os.Stdout)
	logger := backend.Logger("CPUMINER")
	logger.SetLevel(level)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutdown signal received")
		cancel()
	}()

	miner := NewMiner(ctx, MinerConfig{
		Pool:     cfg.pool,
		User:     cfg.user,
		Password: cfg.password,
		Net:      params,
		Threads:  cfg.threads,
		Log:      logger,
	})

	logger.Infof("monetarium-cpuminer %s starting (%s, %d thread(s))", version,
		cfg.net, cfg.threads)
	if err := miner.Run(); err != nil {
		logger.Errorf("miner failed: %v", err)
		os.Exit(1)
	}
	logger.Info("monetarium-cpuminer shutdown complete")
}
