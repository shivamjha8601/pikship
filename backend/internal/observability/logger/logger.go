// Package logger constructs the slog.Logger that every Pikshipp process uses.
//
// Per LLD §02-infrastructure/03-observability: structured JSON to stdout,
// scraped by Vector → CloudWatch (ADR-0011). Domain code accepts a
// *slog.Logger and never reaches for a global.
package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Options drive logger construction.
type Options struct {
	Level   string // "debug" | "info" | "warn" | "error"
	Version string // attached as a "version" attribute on every record
}

// New returns a JSON slog.Logger writing to stdout.
func New(opts Options) (*slog.Logger, error) {
	lvl, err := parseLevel(opts.Level)
	if err != nil {
		return nil, err
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	})
	log := slog.New(h)
	if opts.Version != "" {
		log = log.With(slog.String("version", opts.Version))
	}
	return log, nil
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("logger: unknown level %q", s)
}
