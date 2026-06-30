package signals

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/pterm/pterm"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	otlploghttp "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

// iso8601 is the console timestamp format, matching grackleclub/log's ISO8601.
const iso8601 = "2006-01-02T15:04:05.000Z"

// Logger builds the fanout slog.Logger: a pterm console handler plus, when lp is
// non-nil, an otelslog handler bridging to lp. It installs no globals and owns
// no lifecycle; the caller created lp and is responsible for lp.Shutdown. Pass
// nil for console-only.
func Logger(cfg Config, lp otellog.LoggerProvider) *slog.Logger {
	level, addSource := cfg.levelSource()

	// The console sink is gated by Config.StderrLevel. The OTLP sink is added
	// without a level on purpose: it ships every severity to SigNoz, which is
	// the source of truth and the place to filter. See Config.StderrLevel.
	handlers := []slog.Handler{consoleHandler(os.Stderr, level, addSource, cfg.Console)}
	if lp != nil {
		handlers = append(handlers, otelslog.NewHandler(
			scopeName,
			otelslog.WithLoggerProvider(lp),
		))
	}
	return slog.New(&fanoutHandler{handlers: handlers})
}

// consoleHandler is the pretty dev sink: a pterm logger, wrapped so a span
// context in the log's ctx renders a short trace_id inline (the dev half of the
// correlation contract). pterm is a global singleton, so configuring color here
// also styles any pterm tables/spinners a consumer prints directly — the
// fleet-wide consistency that signals is here to provide.
func consoleHandler(w io.Writer, level slog.Level, addSource bool, c Console) slog.Handler {
	if noColor(w) {
		pterm.DisableColor()
	}
	logger := pterm.DefaultLogger.
		WithWriter(w).
		WithLevel(ptermLevel(level)).
		WithTime(!c.NoTime).
		WithTimeFormat(iso8601).
		WithCaller(addSource)
	if addSource {
		// pterm's slog bridge assumes a direct slog->pterm call (offset 3); the
		// fanout and trace wrappers add two frames between, so bump the offset
		// to land on the caller's source rather than our plumbing.
		logger = logger.WithCallerOffset(callerOffset)
	}
	if c.MaxWidth != 0 {
		logger = logger.WithMaxWidth(c.MaxWidth)
	}
	return newTraceHandler(&ptermHandler{logger: logger})
}

// callerOffset is the stack distance pterm walks from logger.Info up to the
// caller: ptermHandler.Handle, traceHandler.Handle, fanoutHandler.Handle, and
// slog's two internal frames.
const callerOffset = 5

// ptermHandler renders slog records through a pterm.Logger. It replaces
// pterm.NewSlogHandler, whose bridge drops grouped key prefixes, keeps only the
// last WithAttrs (so chained .With loses earlier fields), and orders fields off
// a map. This honors slog's contract instead: attrs accumulate, groups prefix
// their keys, and field order is preserved. Output is otherwise pterm-native —
// it calls the same logger.Args/Info path.
type ptermHandler struct {
	logger  *pterm.Logger
	groups  []string
	preArgs []any // bound attrs, already flattened to prefixed key/value pairs
}

func (h *ptermHandler) Enabled(_ context.Context, level slog.Level) bool {
	return h.logger.CanPrint(ptermLevel(level))
}

func (h *ptermHandler) Handle(_ context.Context, r slog.Record) error {
	args := slices.Clip(h.preArgs)
	prefix := h.prefix()
	r.Attrs(func(a slog.Attr) bool {
		args = appendArg(args, prefix, a)
		return true
	})
	la := h.logger.Args(args...)
	switch {
	case r.Level >= slog.LevelError:
		h.logger.Error(r.Message, la)
	case r.Level >= slog.LevelWarn:
		h.logger.Warn(r.Message, la)
	case r.Level >= slog.LevelInfo:
		h.logger.Info(r.Message, la)
	default:
		h.logger.Debug(r.Message, la)
	}
	return nil
}

func (h *ptermHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	nh := *h
	nh.preArgs = slices.Clip(h.preArgs)
	prefix := h.prefix()
	for _, a := range attrs {
		nh.preArgs = appendArg(nh.preArgs, prefix, a)
	}
	return &nh
}

func (h *ptermHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nh := *h
	nh.groups = append(slices.Clip(h.groups), name)
	return &nh
}

func (h *ptermHandler) prefix() string {
	if len(h.groups) == 0 {
		return ""
	}
	return strings.Join(h.groups, ".") + "."
}

// appendArg flattens an attr onto the key/value slice pterm.Args expects,
// prefixing the key with the open group path and recursing into groups (an
// empty-key group inlines its members, matching slog).
func appendArg(args []any, prefix string, a slog.Attr) []any {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return args
	}
	key := prefix + a.Key
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		if len(group) == 0 {
			return args
		}
		if a.Key != "" {
			key += "."
		} else {
			key = prefix
		}
		for _, ga := range group {
			args = appendArg(args, key, ga)
		}
		return args
	}
	return append(args, key, a.Value.Any())
}

// ptermLevel maps an slog threshold onto pterm's coarser level enum, rounding a
// custom level down to the nearest standard severity.
func ptermLevel(l slog.Level) pterm.LogLevel {
	switch {
	case l <= slog.LevelDebug:
		return pterm.LogLevelDebug
	case l < slog.LevelWarn:
		return pterm.LogLevelInfo
	case l < slog.LevelError:
		return pterm.LogLevelWarn
	default:
		return pterm.LogLevelError
	}
}

// noColor decides colorization: NO_COLOR forces it off; CLICOLOR_FORCE forces
// it on (e.g. `bin/test pretty`, where go test pipes stderr so it isn't a TTY);
// otherwise color only a real terminal.
func noColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return true
	}
	if os.Getenv("CLICOLOR_FORCE") != "" {
		return false
	}
	return !isTerminal(w)
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
