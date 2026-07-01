package signals

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/pterm/pterm"
	"go.opentelemetry.io/otel/trace"
)

func spanContext() context.Context {
	tid, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	sid, _ := trace.SpanIDFromHex("0123456789abcdef")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

// TestTraceHandler_TopLevelTraceID locks the correlation rendering: the
// trace_id stays at the top level even when the caller opened a group, while
// the caller's own attrs remain grouped.
func TestTraceHandler_TopLevelTraceID(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newTraceHandler(slog.NewTextHandler(&buf, nil)))

	log.InfoContext(spanContext(), "plain")
	if got := buf.String(); !strings.Contains(got, "trace_id=01234567") {
		t.Errorf("plain: want top-level trace_id, got: %q", got)
	}

	buf.Reset()
	log.WithGroup("req").InfoContext(spanContext(), "grouped", "method", "GET")
	got := buf.String()
	if !strings.Contains(got, "trace_id=01234567") || strings.Contains(got, "req.trace_id") {
		t.Errorf("grouped: want top-level trace_id (not nested under req), got: %q", got)
	}
	if !strings.Contains(got, "req.method=GET") {
		t.Errorf("grouped: caller attr should stay grouped, got: %q", got)
	}
}

// TestTraceHandler_NoSpan: without a span context, no trace_id is added.
func TestTraceHandler_NoSpan(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newTraceHandler(slog.NewTextHandler(&buf, nil)))

	log.Info("no ctx")
	if strings.Contains(buf.String(), "trace_id") {
		t.Errorf("want no trace_id without a span, got: %q", buf.String())
	}
}

// TestPtermHandler_GroupsAndAttrs locks the reason ptermHandler exists over
// pterm's own bridge: open groups prefix their keys, and chained With accumulates
// rather than dropping all but the last attr.
func TestPtermHandler_GroupsAndAttrs(t *testing.T) {
	pterm.DisableColor()
	var buf bytes.Buffer
	lg := pterm.DefaultLogger.WithWriter(&buf).WithLevel(pterm.LogLevelDebug).WithTime(false)
	log := slog.New(&ptermHandler{logger: lg})

	log.WithGroup("req").Info("grouped", "method", "GET")
	if got := buf.String(); !strings.Contains(got, "req.method") {
		t.Errorf("grouped: want req.method prefix, got: %q", got)
	}

	buf.Reset()
	log.With("a", 1).With("b", 2).Info("chained")
	got := buf.String()
	if !strings.Contains(got, "a:") || !strings.Contains(got, "b:") {
		t.Errorf("chained: want both a and b, got: %q", got)
	}
}
