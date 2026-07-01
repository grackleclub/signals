package signals

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/pterm/pterm"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	otlploghttp "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

// iso8601 is the console timestamp format: local time with no zone designator
// (valid ISO 8601, though not RFC 3339), since pterm renders time.Now() in
// local time and a fixed Z would lie about the zone. The console is a dev
// convenience; OTLP carries the authoritative absolute timestamp, which SigNoz
// shows in UTC.
const iso8601 = "2006-01-02T15:04:05.000"

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
	// pterm colorizes through a process-global (gookit) with no per-stream
	// toggle. NO_COLOR means "nowhere", so disable it globally; but a plain
	// console (a non-TTY stderr) must not disable color for pterm output a
	// consumer sends to its own terminal (e.g. tables on stdout), so strip the
	// color on this stream only rather than reaching for the global.
	switch {
	case os.Getenv("NO_COLOR") != "":
		pterm.DisableColor()
	case os.Getenv("CLICOLOR_FORCE") != "":
		// force color even to a pipe (e.g. bin/test pretty)
	case !isTerminal(w):
		w = &plainWriter{w}
	}
	// Caller is not delegated to pterm's WithCaller: pterm walks the stack by a
	// fixed offset, which our fanout/trace wrappers throw off. ptermHandler adds
	// it from the record PC (the true call site) as a normal arg instead, so it
	// also aligns with the other args in the tree.
	logger := pterm.DefaultLogger.
		WithWriter(w).
		WithLevel(ptermLevel(level)).
		WithTime(c.Time.show(w)).
		WithTimeFormat(iso8601)
	if c.Compact && c.MaxWidth != 0 {
		logger = logger.WithMaxWidth(c.MaxWidth)
	}
	h := &ptermHandler{logger: logger, expand: !c.Compact, caller: addSource}
	if h.expand {
		h.prefixWidth = prefixWidth(logger)
	}
	return newTraceHandler(h)
}

// prefixWidth is the display width pterm renders before the message: the 5-wide
// level tag and its trailing space, plus the fixed-width timestamp and its
// space when shown. The timestamp width is constant for iso8601, so this is
// computed once per handler rather than per record.
func prefixWidth(l *pterm.Logger) int {
	w := 6 // "%-5s" level tag + trailing space
	if l.ShowTime {
		w += runewidth.StringWidth(time.Now().Format(l.TimeFormat)) + 1
	}
	return w
}

// sgr matches ANSI SGR (color/style) escape sequences, the only control codes
// pterm's logger emits to its writer.
var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// plainWriter strips SGR sequences from each write so a captured (non-TTY)
// console stays plain without disabling pterm's global color. pterm writes one
// full line per record, so a sequence never straddles two writes.
type plainWriter struct{ w io.Writer }

func (p *plainWriter) Write(b []byte) (int, error) {
	if _, err := p.w.Write(sgr.ReplaceAll(b, nil)); err != nil {
		return 0, err
	}
	return len(b), nil
}

// ptermHandler renders slog records through a pterm.Logger. It replaces
// pterm.NewSlogHandler, whose bridge drops grouped key prefixes, keeps only the
// last WithAttrs (so chained .With loses earlier fields), and orders fields off
// a map. This honors slog's contract instead: attrs accumulate, groups prefix
// their keys, and field order is preserved. Output is otherwise pterm-native —
// it calls the same logger.Args/Info path.
type ptermHandler struct {
	logger      *pterm.Logger
	expand      bool // size each line to its message so every arg trees
	caller      bool // append the record's source location as a "caller" arg
	prefixWidth int  // rendered width before the message, for expand sizing
	groups      []string
	preArgs     []any // bound attrs, already flattened to prefixed key/value pairs
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
	if h.caller {
		args = appendCaller(args, r.PC)
	}
	logger := h.logger
	if h.expand {
		// Size MaxWidth to exactly the prefix plus message: the message still
		// fits (no message wrap) while any arg overflows, so every arg drops to
		// pterm's tree. See renderColorful's two width checks.
		args = alignArgs(args)
		logger = logger.WithMaxWidth(h.prefixWidth + msgWidth(r.Message))
	}
	la := logger.Args(args...)
	switch {
	case r.Level >= slog.LevelError:
		logger.Error(r.Message, la)
	case r.Level >= slog.LevelWarn:
		logger.Warn(r.Message, la)
	case r.Level >= slog.LevelInfo:
		logger.Info(r.Message, la)
	default:
		logger.Debug(r.Message, la)
	}
	return nil
}

// alignArgs left-pads each value so the values line up in a column while each
// colon stays tight against its key. Keys are untouched, so pterm's KeyStyles
// (err/error/caller) still apply. It returns args unchanged when there is
// nothing to align (fewer than two keys, or all keys the same width), so the
// common path allocates nothing.
func alignArgs(args []any) []any {
	widest, narrowest := 0, -1
	for i := 0; i+1 < len(args); i += 2 {
		k, ok := args[i].(string)
		if !ok {
			continue
		}
		w := runewidth.StringWidth(k)
		if w > widest {
			widest = w
		}
		if narrowest < 0 || w < narrowest {
			narrowest = w
		}
	}
	if narrowest < 0 || widest == narrowest {
		return args
	}
	out := slices.Clone(args)
	for i := 0; i+1 < len(out); i += 2 {
		k, ok := out[i].(string)
		if !ok {
			continue
		}
		if pad := widest - runewidth.StringWidth(k); pad > 0 {
			out[i+1] = strings.Repeat(" ", pad) + pterm.Sprint(out[i+1])
		}
	}
	return out
}

// appendCaller adds the record's source location as a trailing "caller" arg,
// styled to match pterm's own caller line (gray value, gray-bold key via the
// default KeyStyles). It reads the location from the record PC — the true call
// site — rather than pterm's stack-offset guess, and being a normal arg it
// aligns with the rest in the tree.
func appendCaller(args []any, pc uintptr) []any {
	if pc == 0 {
		return args
	}
	f, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	if f.File == "" {
		return args
	}
	return append(args, "caller", pterm.FgGray.Sprint(fmt.Sprintf("%s:%d", f.File, f.Line)))
}

// msgWidth is the display width of the widest line in msg.
func msgWidth(msg string) int {
	width := 0
	for _, line := range strings.Split(msg, "\n") {
		if w := runewidth.StringWidth(line); w > width {
			width = w
		}
	}
	return width
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
