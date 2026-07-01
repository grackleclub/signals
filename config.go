package signals

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Config controls a Setup. Every field has a sane default, so
// signals.Setup(ctx, signals.Config{Env: "prod"}) is a complete call. Config
// fields override OTEL_* env vars, which override built-in defaults.
type Config struct {
	// Env sets deployment.environment.name on every signal (e.g. "prod",
	// "staging", "dev"); the primary SigNoz dimension. Defaults to the value in
	// OTEL_RESOURCE_ATTRIBUTES if set, otherwise "unknown".
	Env string

	// Service overrides the service name. Defaults to OTEL_SERVICE_NAME, then
	// the os.Args[0] basename.
	Service string

	// Version sets service.version (e.g. a build tag / git sha). Optional.
	Version string

	// Endpoint overrides the OTLP/HTTP endpoint and must be a full http(s) URL
	// (e.g. "https://ingest.example.com"). When empty, the exporters read
	// OTEL_EXPORTER_OTLP[_{TRACES,METRICS,LOGS}]_ENDPOINT directly. With neither
	// set, OTLP sinks are not installed (console-only — "graceful off").
	Endpoint string

	// Token, when non-empty, is sent as "Authorization: Bearer <token>", merged
	// over OTEL_EXPORTER_OTLP_HEADERS. Defaults to OTLP_INGEST_TOKEN.
	Token string

	// StderrLevel is the threshold for the pterm console (stderr) sink only. The
	// OTLP sink intentionally exports every level — SigNoz is the source of
	// truth and filters there. Default INFO; the DEBUG env var lifts it to
	// DEBUG + source.
	StderrLevel slog.Level

	// DisableRuntimeMetrics turns off the bundled Go runtime metrics (GC,
	// goroutines, heap), which are collected by default.
	DisableRuntimeMetrics bool

	// Console tunes the pterm console (stderr) sink. The zero value is the
	// drop-in default: timestamps auto (on only when stderr is captured), caller
	// off (unless DEBUG), pterm's own line wrapping.
	Console Console
}

// TimeMode controls the leading console timestamp.
type TimeMode int

const (
	// TimeAuto shows the timestamp only when stderr is not an interactive
	// terminal: hidden in a local TTY, where you have your own clock and
	// ephemeral scrollback, and shown when the output is captured (CI, a file,
	// journald), where it needs an embedded time. It tracks the same TTY signal
	// as color. This is the zero value.
	TimeAuto TimeMode = iota
	// TimeOn always prints the timestamp.
	TimeOn
	// TimeOff never prints the timestamp.
	TimeOff
)

// show resolves whether to print the timestamp; tty reports whether the sink is
// an interactive terminal.
func (m TimeMode) show(tty bool) bool {
	switch m {
	case TimeOn:
		return true
	case TimeOff:
		return false
	default:
		return !tty
	}
}

// Layout controls how a record's args are arranged on the console.
type Layout int

const (
	// LayoutAuto trees the args (one per line, values aligned) on a terminal or
	// in GitHub Actions, where multi-line reads well, and keeps the whole record
	// on one line otherwise — journald, files, and other line-oriented capture,
	// where each newline becomes a separate entry. This is the zero value.
	LayoutAuto Layout = iota
	// LayoutTree always puts each arg on its own line.
	LayoutTree
	// LayoutOneline always keeps the whole record on one line.
	LayoutOneline
)

// expand reports whether to use the tree layout; rich reports whether the sink
// renders multi-line well (a terminal or GitHub Actions).
func (l Layout) expand(rich bool) bool {
	switch l {
	case LayoutTree:
		return true
	case LayoutOneline:
		return false
	default:
		return rich
	}
}

// Console configures the pterm-backed console sink, the pretty half of the
// logger. It only shapes stderr output; the OTLP sink is unaffected. pterm is
// a process-global singleton, so the styling signals applies here (writer,
// color) is shared by every pterm component a consumer uses directly — tables,
// spinners, and the rest. That shared state is the point: one consistent look
// across the fleet with no per-call wiring.
type Console struct {
	// Time controls the leading wall-clock timestamp. The zero value (TimeAuto)
	// shows it only when stderr is captured rather than a local terminal.
	Time TimeMode

	// Caller forces source locations on even without DEBUG (which already
	// turns them on together with debug level). The location is read from the
	// record's PC (the true call site) and rendered as a trailing "caller" arg.
	Caller bool

	// Layout arranges a record's args as a tree (one per line, values aligned)
	// or on one line. The zero value (LayoutAuto) chooses per environment: a
	// tree on a terminal or in GitHub Actions, one line for other captured
	// output such as journald, where a multi-line record fragments into
	// separate entries.
	Layout Layout
}

// resolved fills empty fields from the standard OTEL_* env vars and defaults.
// Config fields win over env; env wins over built-in defaults. Endpoint is
// deliberately NOT folded in from env here — leaving Config.Endpoint empty lets
// the exporters honor OTEL_EXPORTER_OTLP[_*]_ENDPOINT natively (per-signal
// precedence, default /v1/{signal} paths). See Config.otlp.
func (c Config) resolved() Config {
	if c.Service == "" {
		c.Service = os.Getenv("OTEL_SERVICE_NAME")
	}
	if c.Service == "" && len(os.Args) > 0 {
		c.Service = filepath.Base(os.Args[0])
	}
	if c.Token == "" {
		c.Token = os.Getenv("OTLP_INGEST_TOKEN")
	}
	return c
}

// otlpEnabled reports whether an OTLP endpoint is configured at all, via Config
// or any of the standard endpoint env vars. When it is not, Setup degrades to
// console-only ("graceful off") — no exporters, no error.
func (c Config) otlpEnabled() bool {
	if c.Endpoint != "" {
		return true
	}
	for _, k := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
	} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// levelSource resolves the console threshold and source toggle. The DEBUG env
// var lifts the level to DEBUG and turns on source locations; otherwise
// Config.StderrLevel applies. (The OTLP sink is unaffected — it ships all
// levels.)
func (c Config) levelSource() (slog.Level, bool) {
	if debugEnv() {
		return slog.LevelDebug, true
	}
	return c.StderrLevel, c.Console.Caller
}

func debugEnv() bool {
	v := strings.ToLower(os.Getenv("DEBUG"))
	return v != "" && v != "0" && v != "false"
}
