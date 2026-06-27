// Package signals is OTel-native observability for grackleclub services:
// logs, metrics, and traces bootstrapped from one Setup call, exported over
// OTLP/HTTP to SigNoz as the single source of truth.
//
// Call Setup once from main: it installs the global tracer, meter, and logger
// providers plus the W3C propagator — all sharing one Resource — and returns a
// ready *slog.Logger and a shutdown that flushes every exporter. Libraries do
// not call Setup; they obtain a tracer/meter from the installed globals via
// otel.Tracer(scope) / otel.Meter(scope).
//
// Logs emitted with a context inside a span are correlated with their trace by
// construction. With no OTLP endpoint configured, signals degrades to a
// console-only logger — no exporters, no error ("graceful off").
//
// See DESIGN.md for the full rationale.
package signals
