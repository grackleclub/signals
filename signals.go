package signals

import (
	"context"
	"errors"
	"log/slog"

	otellog "go.opentelemetry.io/otel/log"
)

// errNotImplemented marks every stub in this skeleton. The TDD test-side ships
// the failing tests first; implementation replaces these returns.
var errNotImplemented = errors.New("signals: not implemented")

// Setup installs the global tracer, meter, and logger providers plus the W3C
// propagator, all sharing one Resource, and returns a ready slog.Logger and a
// shutdown that flushes/closes every exporter.
//
// Call once from main; defer the shutdown. Libraries must not call Setup —
// they obtain a tracer/meter from the installed globals via otel.Tracer(scope)
// / otel.Meter(scope).
func Setup(ctx context.Context, cfg Config) (
	shutdown func(context.Context) error,
	logger *slog.Logger,
	err error,
) {
	return nil, nil, errNotImplemented
}

// Logger builds the fanout slog.Logger: a tint console handler plus, when lp is
// non-nil, an otelslog handler bridging to lp. It installs no globals and owns
// no lifecycle; the caller created lp and is responsible for lp.Shutdown. Pass
// nil for console-only.
func Logger(cfg Config, lp otellog.LoggerProvider) *slog.Logger {
	return nil
}
