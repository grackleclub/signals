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

	// StderrLevel is the threshold for the tint console (stderr) sink only. The
	// OTLP sink intentionally exports every level — SigNoz is the source of
	// truth and filters there. Default INFO; the DEBUG env var lifts it to
	// DEBUG + source.
	StderrLevel slog.Level

	// DisableRuntimeMetrics turns off the bundled Go runtime metrics (GC,
	// goroutines, heap), which are collected by default.
	DisableRuntimeMetrics bool
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
	return c.StderrLevel, false
}

func debugEnv() bool {
	v := strings.ToLower(os.Getenv("DEBUG"))
	return v != "" && v != "0" && v != "false"
}
