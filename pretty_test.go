package signals_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/grackleclub/signals"
	"go.opentelemetry.io/otel/trace"
)

// TestPretty is the kitchen sink: it emits a record at every level and a wide
// spread of attribute shapes, inside a span context, so the pterm console
// output is inspectable when the test runs:
//
//	./bin/test pretty      # local (no time) and CI (time) variants
//
// It is not a golden-file assertion — the value is eyeballing the pretty
// output (color, timestamp, inline trace_id correlation). SIGNALS_PRETTY_TIME
// forces the timestamp on/off (unset = the TimeAuto default); the harness shows
// both the local and captured looks.
func TestPretty(t *testing.T) {
	log := signals.Logger(signals.Config{
		StderrLevel: slog.LevelDebug,
		Console:     signals.Console{Time: prettyTime()},
	}, nil)
	if log == nil {
		t.Fatal("Logger returned nil")
	}

	// A fixed span context so the console handler renders a short trace_id
	// inline, demonstrating the correlation contract in dev.
	tid, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	sid, _ := trace.SpanIDFromHex("0123456789abcdef")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	// Every level.
	log.DebugContext(ctx, "debug: fine-grained detail", "count", 1)
	log.InfoContext(ctx, "info: the normal case", "addr", ":8080", "ready", true)
	log.WarnContext(ctx, "warn: something to watch", "retries", 3)
	log.ErrorContext(ctx, "error: it broke", "err", errors.New("boom"))

	// A spread of attribute shapes / a group.
	log.InfoContext(ctx, "kitchen sink",
		slog.String("string", "value"),
		slog.Int("int", 42),
		slog.Float64("float", 3.14),
		slog.Bool("bool", true),
		slog.Duration("dur", 0),
		slog.Group("nested",
			slog.String("inner", "x"),
			slog.Int("n", 7),
		),
	)

	// A child logger with bound attributes, no ctx (uncorrelated on purpose).
	log.With("component", "demo").Warn("uncorrelated: no ctx, no trace_id")
}

// prettyTime reads SIGNALS_PRETTY_TIME so the harness can force the timestamp
// on (the captured/CI look) or off (the local look); unset is the TimeAuto
// default, which under go test's piped stderr resolves to on.
func prettyTime() signals.TimeMode {
	switch os.Getenv("SIGNALS_PRETTY_TIME") {
	case "off":
		return signals.TimeOff
	case "on":
		return signals.TimeOn
	default:
		return signals.TimeAuto
	}
}
