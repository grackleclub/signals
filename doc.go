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
// The console sink is a pterm logger. Because pterm is a process-global
// singleton, the styling signals sets is shared by any pterm component a
// consumer prints directly (tables, spinners, boxes), so they match the logs
// with no wiring — see examples.md. signals renders through its own slog
// handler rather than pterm's bundled bridge, so attribute order is preserved,
// open groups prefix their keys, and chained With accumulates. Tune the sink
// via Config.Console.
package signals
