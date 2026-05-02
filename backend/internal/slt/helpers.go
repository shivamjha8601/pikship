package slt

import (
	"log/slog"
	"io"
)

// NopLogger returns a slog.Logger that discards all output — keeps test
// output clean while still satisfying service constructors.
func NopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
