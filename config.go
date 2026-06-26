package signals

import "log/slog"

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
	// OTEL_EXPORTER_OTLP_ENDPOINT, then the public ingest host. Empty after
	// resolution => OTLP sinks are not installed (console-only).
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
