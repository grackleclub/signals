package signals

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	"github.com/lmittmann/tint"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	otlploghttp "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

// iso8601 is the console timestamp format, matching grackleclub/log's ISO8601.
const iso8601 = "2006-01-02T15:04:05.000Z"

// Logger builds the fanout slog.Logger: a tint console handler plus, when lp is
// non-nil, an otelslog handler bridging to lp. It installs no globals and owns
// no lifecycle; the caller created lp and is responsible for lp.Shutdown. Pass
// nil for console-only.
func Logger(cfg Config, lp otellog.LoggerProvider) *slog.Logger {
	level, addSource := cfg.levelSource()

	// The console sink is gated by Config.StderrLevel. The OTLP sink is added
	// without a level on purpose: it ships every severity to SigNoz, which is
	// the source of truth and the place to filter. See Config.StderrLevel.
	handlers := []slog.Handler{consoleHandler(os.Stderr, level, addSource)}
	if lp != nil {
		handlers = append(handlers, otelslog.NewHandler(
			scopeName,
			otelslog.WithLoggerProvider(lp),
		))
	}
	return slog.New(&fanoutHandler{handlers: handlers})
}

// consoleHandler is the pretty dev sink: tint, wrapped so a span context in the
// log's ctx renders a short trace_id inline (the dev half of the correlation
// contract — see DESIGN.md "logging").
func consoleHandler(w io.Writer, level slog.Leveler, addSource bool) slog.Handler {
	h := tint.NewHandler(w, &tint.Options{
		Level:      level,
		AddSource:  addSource,
		TimeFormat: iso8601,
		NoColor:    os.Getenv("NO_COLOR") != "" || !isTerminal(w),
	})
	return newTraceHandler(h)
}

// isTerminal reports whether w is a character device (a TTY), so color is only
// emitted to a real terminal — not to a pipe or a captured test buffer.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// traceHandler decorates records with a short trace_id when the ctx carries a
// valid span context. Only the console path uses it; otelslog attaches full
// trace context to OTLP records natively.
//
// The trace_id is kept at the top level even when the caller has opened a
// group: in the common (no-group) case it is appended to the record; once a
// group is open it is injected at the ungrouped base and the caller's
// groups/attrs are replayed over it. The record handed to Handle is already a
// private copy (fanoutHandler clones per child), so it is mutated in place.
type traceHandler struct {
	inner   slog.Handler                      // base + caller mods, for emit
	base    slog.Handler                      // ungrouped base, for top-level id
	mods    []func(slog.Handler) slog.Handler // caller WithAttrs/WithGroup, in order
	grouped bool                              // any WithGroup applied
}

func newTraceHandler(base slog.Handler) *traceHandler {
	return &traceHandler{inner: base, base: base}
}

func (h *traceHandler) with(mod func(slog.Handler) slog.Handler, group bool) *traceHandler {
	return &traceHandler{
		inner:   mod(h.inner),
		base:    h.base,
		mods:    append(slices.Clip(h.mods), mod),
		grouped: h.grouped || group,
	}
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return h.inner.Handle(ctx, r)
	}
	id := slog.String("trace_id", shortID(sc.TraceID().String()))
	if !h.grouped {
		r.AddAttrs(id)
		return h.inner.Handle(ctx, r)
	}
	emit := h.base.WithAttrs([]slog.Attr{id})
	for _, m := range h.mods {
		emit = m(emit)
	}
	return emit.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.with(func(x slog.Handler) slog.Handler { return x.WithAttrs(attrs) }, false)
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.with(func(x slog.Handler) slog.Handler { return x.WithGroup(name) }, true)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// fanoutHandler dispatches each record to every enabled child handler, so logs
// reach the console and OTLP from one slog call. Per-handler Enabled gating
// lets each sink keep its own threshold.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: hs}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return f
	}
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithGroup(name)
	}
	return &fanoutHandler{handlers: hs}
}

// loggerProvider builds the OTLP/HTTP LoggerProvider. The caller installs it as
// the global and owns its Shutdown.
func loggerProvider(ctx context.Context, oc otlpConfig, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	var opts []otlploghttp.Option
	if oc.host != "" {
		opts = append(opts, otlploghttp.WithEndpoint(oc.host))
		if oc.insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
		if oc.path != "" {
			opts = append(opts, otlploghttp.WithURLPath(oc.path))
		}
	}
	if len(oc.headers) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(oc.headers))
	}
	exp, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otlp log exporter: %w", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
	)
	return lp, nil
}
