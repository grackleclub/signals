package signals

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	otlploghttp "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

// tintTimeFormat is ISO8601 with millisecond precision.
const tintTimeFormat = "2006-01-02T15:04:05.000Z07:00"

// Logger builds the fanout slog.Logger: a tint console handler plus, when lp is
// non-nil, an otelslog handler bridging to lp. It installs no globals and owns
// no lifecycle; the caller created lp and is responsible for lp.Shutdown. Pass
// nil for console-only.
func Logger(cfg Config, lp otellog.LoggerProvider) *slog.Logger {
	level, addSource := cfg.levelSource()

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
		TimeFormat: tintTimeFormat,
		NoColor:    os.Getenv("NO_COLOR") != "" || !isTerminal(w),
	})
	return &traceHandler{Handler: h}
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
type traceHandler struct {
	slog.Handler
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r = r.Clone()
		r.AddAttrs(slog.String("trace_id", shortID(sc.TraceID().String())))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
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
func loggerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	var opts []otlploghttp.Option
	if host, insecure, path := endpointParts(cfg.Endpoint); host != "" {
		opts = append(opts, otlploghttp.WithEndpoint(host))
		if insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
		if path != "" {
			opts = append(opts, otlploghttp.WithURLPath(path))
		}
	}
	if cfg.Token != "" {
		opts = append(opts, otlploghttp.WithHeaders(bearer(cfg.Token)))
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
