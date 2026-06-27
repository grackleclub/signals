package signals_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/grackleclub/signals"
)

// These unit tests need no collector and no network. They pin the console-only
// contract: with no OTLP endpoint resolved, signals degrades to a working
// console logger with no error ("graceful off").

func TestSetup_GracefulOff(t *testing.T) {
	// No endpoint, no OTEL_* env => console-only, no exporters, no error.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	ctx := context.Background()

	shutdown, log, err := signals.Setup(ctx, signals.Config{Env: "test"})
	if err != nil {
		t.Fatalf("Setup graceful-off: unexpected error: %v", err)
	}
	if log == nil {
		t.Fatal("Setup returned nil logger")
	}
	if shutdown == nil {
		t.Fatal("Setup returned nil shutdown")
	}
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestLogger_ConsoleOnly(t *testing.T) {
	// Logger(cfg, nil) wires console-only and must hand back a usable logger.
	log := signals.Logger(signals.Config{Env: "test", StderrLevel: slog.LevelInfo}, nil)
	if log == nil {
		t.Fatal("Logger(cfg, nil) returned nil")
	}
	// Must not panic when used.
	log.Info("console-only logger works")
}
