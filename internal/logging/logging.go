package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	charmlog "github.com/charmbracelet/log"
)

type ctxKey struct{}

// New returns a slog.Logger backed by charmbracelet/log.
func New(level slog.Level) *slog.Logger {
	h := charmlog.NewWithOptions(os.Stderr, charmlog.Options{
		ReportTimestamp: true,
		ReportCaller:    false,
		Level:           charmlog.Level(level),
	})
	return slog.New(h)
}

// Into stores the logger on ctx.
func Into(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From retrieves the logger from ctx, or returns the default if absent.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// ParseLevel converts a config string into a slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q", s)
	}
}
