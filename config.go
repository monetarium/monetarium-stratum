package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jessevdk/go-flags"
)

// config holds the stratum pool server configuration.
type config struct {
	HomeDir            string `long:"appdata" short:"A" description:"Path to application home directory"`
	ConfigFile         string `long:"configfile" description:"Path to configuration file"`
	DataDir            string `long:"datadir" description:"Directory to store data"`
	Listen             string `long:"listen" short:"l" default:"0.0.0.0:5550" description:"Address to listen for miner connections"`
	NodeRPC            string `long:"noderpc" default:"127.0.0.1:9509" description:"Host:port of the monetarium-node RPC server"`
	RPCUser            string `long:"rpcuser" default:"user" description:"Node RPC username"`
	RPCPass            string `long:"rpcpass" description:"Node RPC password"`
	RPCCert            string `long:"rpccert" description:"Path to the node RPC TLS certificate"`
	ShareDifficulty    uint32 `long:"sharedifficulty" default:"100" description:"Pool share difficulty"`
	BlockSubmitDivisor uint32 `long:"blocksubmitdivisor" default:"1" description:"Submit only 1 in every N solved blocks to the network (1 disables throttling)"`
	PoolPassword       string `long:"poolpassword" description:"Password required to authorize workers (empty accepts any worker)"`
	DebugLevel         string `long:"debuglevel" default:"info" description:"Logging level (trace, debug, info, warn, error, critical)"`
	MaxClients         int    `long:"maxclients" default:"10" description:"Maximum number of connected miners"`
}

// defaultConfig returns the configuration populated with default home dirs.
func defaultConfig() (*config, error) {
	home := cleanAndExpandPath("~")
	cfg := &config{}
	cfg.HomeDir = home
	cfg.ConfigFile = filepath.Join(home, ".monetarium-stratum", "monetarium-stratum.conf")
	cfg.DataDir = filepath.Join(home, ".monetarium-stratum")
	cfg.RPCCert = filepath.Join(home, ".monetarium", "rpc.cert")
	return cfg, nil
}

// parseConfig parses command line flags and the configuration file.
func parseConfig() (*config, error) {
	cfg, err := defaultConfig()
	if err != nil {
		return nil, err
	}

	parser := flags.NewParser(cfg, flags.Default)
	_, err = parser.Parse()
	if err != nil {
		return nil, err
	}

	// Load the configuration file if it exists.
	cfg.ConfigFile = cleanAndExpandPath(cfg.ConfigFile)
	if fileExists(cfg.ConfigFile) {
		iniParser := flags.NewIniParser(parser)
		if err := iniParser.ParseFile(cfg.ConfigFile); err != nil {
			return nil, fmt.Errorf("error parsing config file %s: %v", cfg.ConfigFile, err)
		}
	}

	return cfg, nil
}

// fileExists reports whether the named file exists.
func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// cleanAndExpandPath expands environment variables and leading tildes in a
// path.
func cleanAndExpandPath(path string) string {
	path = os.ExpandEnv(path)
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		path = home + path[1:]
	}
	return filepath.Clean(path)
}
