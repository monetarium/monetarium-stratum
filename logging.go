package main

import (
	"os"

	"github.com/decred/slog"
)

// setupLogger creates a package logger at the given level.
func setupLogger(debugLevel string) slog.Logger {
	level, _ := slog.LevelFromString(debugLevel)
	backend := slog.NewBackend(os.Stdout)
	logger := backend.Logger("STRM")
	logger.SetLevel(level)
	return logger
}
