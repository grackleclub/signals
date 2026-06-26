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
	// "staging", "dev"). Required — it's the primary SigNoz dimension.
	Env string

	// Service overrides the service name. Defaults to OTEL_SERVICE_NAME, then
	// the os.Args[0] basename.
	Service string

	// Version sets service.version (e.g. a build tag / git sha). Optional.
	Version string

	// Endpoint overrides the OTLP/HTTP endpoint. Defaults to
	// OTEL_EXPORTER_OTLP_ENDPOINT. Empty after resolution (and no per-signal
	// endpoint set) => OTLP sinks are not installed (console-only).
	Endpoint string

	// Token, when non-empty, is sent as "Authorization: Bearer <token>" to
	// satisfy the ingest auth gate. Defaults to OTLP_INGEST_TOKEN.
	Token string

	// Level is the threshold for both sinks. Default INFO; the DEBUG env var
	// lifts it to DEBUG + source.
	Level slog.Level

	// DisableRuntimeMetrics turns off the bundled Go runtime metrics (GC,
	// goroutines, heap), which are collected by default.
	DisableRuntimeMetrics bool
}

// resolved fills empty fields from the standard OTEL_* env vars and defaults.
// Config fields win over env; env wins over built-in defaults.
func (c Config) resolved() Config {
	if c.Service == "" {
		c.Service = os.Getenv("OTEL_SERVICE_NAME")
	}
	if c.Service == "" && len(os.Args) > 0 {
		c.Service = filepath.Base(os.Args[0])
	}
	if c.Endpoint == "" {
		c.Endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if c.Token == "" {
		c.Token = os.Getenv("OTLP_INGEST_TOKEN")
	}
	return c
}

// otlpEnabled reports whether an OTLP endpoint is configured at all. When it is
// not, Setup degrades to console-only ("graceful off") — no exporters, no
// error. Mirrors the per-signal env vars the OTLP SDK honors itself.
func (c Config) otlpEnabled() bool {
	if c.Endpoint != "" {
		return true
	}
	for _, k := range []string{
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

// levelSource resolves the log threshold and source toggle. The DEBUG env var
// lifts the level to DEBUG and turns on source locations, matching log's
// existing behavior; otherwise Config.Level applies.
func (c Config) levelSource() (slog.Level, bool) {
	if debugEnv() {
		return slog.LevelDebug, true
	}
	return c.Level, false
}

func debugEnv() bool {
	v := strings.ToLower(os.Getenv("DEBUG"))
	return v != "" && v != "0" && v != "false"
}
