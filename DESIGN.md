# signals — design

`github.com/grackleclub/signals`

OTel-native observability for grackleclub services: logs, metrics, and traces bootstrapped from one call, exported over OTLP/HTTP to SigNoz as the single source of truth. Replaces the logging role of [grackleclub/log](https://github.com/grackleclub/log).

This is a design proposal — API shapes and rationale, no implementation yet. The design is validated against `grackleclub/rulette`, whose existing `otel.go` is effectively this package's prototype (see [migration](#migration--retirement)).

## goals

- **One consistent bootstrap** for every project: logs + metrics + traces with a single `Setup`, so new services start instrumented by default.
- **SigNoz as single source of truth** — all three signals leave the process as OTLP, sharing one identity (`Resource`) so SigNoz can join them.
- **Trace-correlated logs** by construction: a log emitted inside a span carries `trace_id`/`span_id` with no per-call effort.
- **Keep the pretty dev experience** — tint console output stays, fully under local control, independent of what ships.
- **Graceful off** — with no endpoint configured, the package degrades to console-only. The same binary runs locally, in CI, and in prod.

## non-goals

- **TUI / spinners / tables.** These stay in `log` for now and later move to a dedicated package. `signals` deliberately excludes them to keep the OTel SDK out of interactive-output code.
- **Tail sampling.** Handled at the SigNoz ingester collector. `signals` only ensures the app exports complete traces (see [sampling](#sampling)).
- **Bespoke config schemes.** We honor the standard `OTEL_*` env vars rather than inventing our own.

## scope boundary

| concern | home |
| --- | --- |
| logs (console + OTLP), metrics, traces, bootstrap | **signals** (this repo) |
| pretty TUI, spinners, tables | `log` now → new `tui` package later |

`signals` depends only on `lmittmann/tint` + the OTel SDK. It does **not** import `grackleclub/log`; the tint console handler (~15 lines of `tint.NewHandler`) is owned here so the import graph stays clean.

## public API (proposed)

```go
package signals

// Setup installs the global tracer, meter, and logger providers plus the W3C
// propagator, all sharing one Resource, and returns a ready slog.Logger and a
// shutdown that flushes/closes every exporter.
//
// Call once from main; defer the shutdown. Libraries must not call Setup —
// they obtain a tracer/meter from the installed globals via otel.Tracer(scope)
// / otel.Meter(scope) (see "instrumentation" below).
func Setup(ctx context.Context, cfg Config) (
    shutdown func(context.Context) error,
    logger  *slog.Logger,
    err     error,
)

// Logger builds the fanout slog.Logger: a tint console handler plus, when lp is
// non-nil, an otelslog handler bridging to lp. It installs no globals and owns
// no lifecycle; the caller created lp and is responsible for lp.Shutdown. Pass
// nil for console-only. Use Logger to configure logging manually when Setup's
// all-in-one bootstrap does not fit. Setup calls Logger with the LoggerProvider
// it builds.
func Logger(cfg Config, lp otellog.LoggerProvider) *slog.Logger
```

### Config

```go
type Config struct {
    // Env sets deployment.environment.name on every signal (e.g. "prod",
    // "staging", "dev"). Required — it's the primary SigNoz dimension.
    Env string

    // Service overrides the service name. Defaults to OTEL_SERVICE_NAME, then
    // os.Args[0] basename.
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

    // StderrLevel is the threshold for the tint console (stderr) sink only.
    // The OTLP sink exports every level — SigNoz is the source of truth and
    // filters there. Default INFO; the DEBUG env var lifts it to DEBUG + source
    // (matching log's existing behavior). Set it per environment.
    StderrLevel slog.Level

    // DisableRuntimeMetrics turns off the bundled Go runtime metrics (GC,
    // goroutines, heap), which are collected by default.
    DisableRuntimeMetrics bool
}
```

Everything has a sane default; `signals.Setup(ctx, signals.Config{Env: "prod"})` is a complete call.

### usage

```go
func main() {
    ctx := context.Background()
    shutdown, log, err := signals.Setup(ctx, signals.Config{
        Env:     "prod",
        Version: build.SHA,
    })
    if err != nil {
        panic(fmt.Errorf("setup signals: %w", err))
    }
    defer shutdown(ctx) // flush telemetry on exit

    // logs (correlated when called with ctx inside a span)
    log.InfoContext(ctx, "starting", "addr", addr)
}
```

`Logger` exists for the rare caller managing OTel themselves (tests, or an app composing its own providers): they own the `LoggerProvider` and its shutdown, and `Logger` just wires the fanout. A worked example belongs in the README, not here.

## instrumentation: globals, and the two `name`s

**Decided: globals-only.** `Setup` installs the global providers (`otel.SetTracerProvider`, etc.); every package gets its tracer/meter from the global. `signals` does **not** expose `signals.Tracer`/`signals.Meter` helpers.

Why: instrumented packages then depend only on the tiny, stable `go.opentelemetry.io/otel` API — not on `signals`. And third-party instrumentation (`otelhttp`, db drivers) reads the global provider regardless, so setting globals makes everything participate automatically. A helper would save nothing and couple the whole codebase to the bootstrap lib.

```go
package game

import "go.opentelemetry.io/otel"

// scope = the instrumentation scope name; convention is this package's
// import path. Distinct from service.name (see below).
const scope = "github.com/grackleclub/rulette/game"

var tracer = otel.Tracer(scope)
var meter  = otel.Meter(scope)

func Play(ctx context.Context) {
    ctx, span := tracer.Start(ctx, "Play")
    defer span.End()
}
```

**Two different `name`s — don't conflate them:**

| name | what it identifies | where set | lands as |
| --- | --- | --- | --- |
| `service.name` | the whole process/service (`rulette`) | once, in `Setup` via `Config.Service` (Resource) | resource attr on every signal |
| scope `name` | the package/component emitting telemetry | per-package, by the caller of `otel.Tracer(name)` | `otel.scope.name` on spans/metrics |

In SigNoz you filter by `service.name` ("show me rulette") and/or `otel.scope.name` ("…its game package"). `signals` owns the first; each package names its own scope (its import path).

## logging: pretty + OTLP, correlated

slog stays the frontend. The logger fans out to two handlers:

```
                          ┌─ tint console handler (stderr)   ← dev sees this
slog.Logger ── fanout ────┤
   (InfoContext)          └─ otelslog → OTLP LoggerProvider   ← ships to SigNoz
```

- **tint console handler** — your existing pretty output: ISO8601, color, `NO_COLOR`, `DEBUG`-driven level/source.
- **otelslog handler** (`go.opentelemetry.io/contrib/bridges/otelslog`) — bridges slog into the OTel LoggerProvider; attaches trace context from `ctx` automatically. Installed only when an endpoint resolves.
- **fanout** — same record/ctx to both; per-handler `Enabled` gating (reuse the `fanoutHandler` design proven in `log`).

### the correlation contract

Two conditions, both handled here so callers can't get it wrong:

1. **Records carry trace context.** otelslog does this natively for OTLP; the tint handler reads `trace.SpanContextFromContext(ctx)` and renders a short `trace_id` so the linkage is visible inline in dev.
2. **Log with context.** Correlation requires `InfoContext`/`ErrorContext`, etc. — the non-context methods have no `ctx` to read. We document this loudly and emit correlated examples; a vet/lint convention to prefer the `…Context` methods is a good follow-up.

This closes the current gap: today neither `log`'s tint nor its custom handler injects `trace_id`, so logs and traces don't join.

## resource — the join key

Built once, shared by all three providers. SigNoz correlates logs/traces/metrics on these:

- `service.name`, `service.version`
- `deployment.environment.name` ← `Config.Env`
- `host.name` / instance id
- plus `OTEL_RESOURCE_ATTRIBUTES` from env

Attribute keys come from the **semconv** package, not string literals, pinned to one version in a single file (see [semconv pinning](#semconv-pinning)). We pin **semconv v1.41**, which emits `deployment.environment.name` (the key was `deployment.environment` until semconv v1.27).

**Cross-repo dependency:** the SigNoz collector's spanmetrics dimensions in `cloud` currently key on the old `deployment.environment`. Emitting `deployment.environment.name` requires updating that collector config in lockstep, or the environment dimension silently splits across two keys. This collector change must land with the first `signals`-instrumented service (see [migration](#migration--retirement)).

## semconv pinning

semconv is versioned and import aliases are per-file, so the version is pinned in exactly one file, `semconv.go`, which re-exports the keys we use as package vars:

```go
// semconv.go — the only file naming a semconv version
package signals

import semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

var (
    keyServiceName    = semconv.ServiceNameKey
    keyServiceVersion = semconv.ServiceVersionKey
    keyDeployEnv      = semconv.DeploymentEnvironmentNameKey // "deployment.environment.name"
)
```

Everything else references these vars (e.g. `keyDeployEnv.String(cfg.Env)`), so a version bump touches one file. Note v1.41 exposes `DeploymentEnvironmentNameKey` as a Key only (use `.String(env)`); `ServiceName`/`ServiceVersion`/`HostName` still have helper funcs. semconv v1.41 pulls otel v1.44, which sets the **Go 1.25** floor (below).

## sampling

Default head sampler: **`ParentBased(AlwaysSample)`** — export every span. Rationale: tail sampling lives at the SigNoz ingester collector, and the `tail_sampling` processor must see all spans of a trace to decide. If the app head-samples, those spans never reach the collector and can't be tail-sampled. Overridable via `OTEL_TRACES_SAMPLER` / `OTEL_TRACES_SAMPLER_ARG` for the rare case cost forces app-side reduction.

## metrics

- **Export interval:** the periodic reader uses the OTel default (60s), overridable via `OTEL_METRIC_EXPORT_INTERVAL`. Not hardcoded.
- **Host metrics** (CPU/mem/disk/net): *not* collected by `signals` — the SigNoz collector agents already scrape these (`collector/agent-config*.yaml`), so emitting them from the app would double-count.
- **Go runtime metrics** (GC, goroutines, heap) via `contrib/instrumentation/runtime`: bundled **on by default**, since consistent per-service runtime visibility is a goal; opt out with `Config.DisableRuntimeMetrics`.
- **App metrics** are the app's own job (e.g. rulette's `cache.hits`), created via `otel.Meter(scope)`.

## transport & config

- **OTLP/HTTP** (not gRPC) — matches the public ingest, which HAProxy fronts on 443 → collector 4318. gRPC (4317) is internal-only.
- **Auth** — bearer token via `Authorization: Bearer <token>`; convention is the bare token in `OTLP_INGEST_TOKEN`, prefix added here. `OTEL_EXPORTER_OTLP_HEADERS` is also honored for callers who set the header directly.
- **Standard env vars honored:** `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_TRACES_SAMPLER`. Config fields override env; env overrides defaults.
- **Graceful off:** OTLP sinks are installed only when an endpoint is configured. Matching rulette's current behavior, "configured" means any of `OTEL_EXPORTER_OTLP_ENDPOINT` or the per-signal `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_ENDPOINT` is set (or `Config.Endpoint`). Otherwise: console handler only, no exporters, no error.

## package layout

```
github.com/grackleclub/signals
  signals.go    Setup, Logger; installs providers + propagator
  resource.go   shared Resource builder
  log.go        fanout logger: tint console + otelslog OTLP, trace-correlated
  config.go     Config, OTEL_* env parsing, defaults
  semconv.go    pinned semconv version + re-exported keys (single bump point)
```

Deps: `lmittmann/tint`, `go.opentelemetry.io/otel` **v1.44** (+ sdk, the three OTLP/HTTP exporters, `contrib/bridges/otelslog`, `contrib/instrumentation/runtime`, semconv v1.41).

**Go floor: 1.25** (otel v1.44 requires it). This raises the floor for every consumer — `cloud` (currently go 1.24) and the apps must bump to Go 1.25 when they adopt `signals`. Accepted. (rulette is already on go 1.25 / otel v1.43.)

## migration & retirement

Additive and incremental — nothing breaks on day one.

1. **Ship `signals`** + flip the SigNoz collector spanmetrics dimension to `deployment.environment.name` in `cloud` (lockstep — see [resource](#resource--the-join-key)). Apps migrate logging `log.New` → `signals.Setup` per-service, bumping to Go 1.25 as they do. Each migrated app gains correlated logs + metrics + traces and can drop its journald-scrape entry once it's confirmed OTLP-exporting.
2. **Ship a new `tui` package** (spinners/tables, possibly pterm). Apps migrate TUI usage off `log`.
3. **Retire `log`.** Only after both (1) and (2) — `log` retirement is *gated* on the tui replacement, because any app using both `log.New` and `log`'s TUI can't drop the dependency until TUI has a new home.

End state: `signals` (telemetry + logging), `tui` (interactive output), `log` retired.

### rulette as the reference migration

rulette already has the full stack (`otel.go`: resource + 3 OTLP/HTTP exporters + 3 providers + propagator + otelslog + shutdown), so adopting `signals` is mostly deletion:

- **Removed** (moves into `signals`): `initOtel`, `otelResource`, `initLogger` + the manual `grackleclub/log` fanout. ~120 lines.
- **Unchanged** (globals-only pays off): the `scope` const, `otel.Meter(scope)`/`otel.Tracer(scope)` call sites, `initMetrics` + the cache instruments + `attr*` keys, and `otelhttp.NewHandler(mux, "rulette")`.
- **`main.go`**: ~4 lines — replace the `initOtel`/`initLogger` pair with one `signals.Setup` call.
- **New behavior**: `deployment.environment.name` (rulette sets `service.name` today but no environment), and `service.version` becomes a Resource attribute instead of a `log.With` attribute.

## open questions

None remaining — see resolved below.

### resolved

- **Console-only level**: `Config.StderrLevel` gates the tint console sink; the OTLP sink ships every level (SigNoz is the source of truth and filters there). The DEBUG env var lifts the console level + source.
- **Metric defaults**: default export interval (env-overridable via `OTEL_METRIC_EXPORT_INTERVAL`); no host metrics (the collector owns them); Go runtime metrics on by default with `Config.DisableRuntimeMetrics` opt-out. See [metrics](#metrics).
- **Logger from `Setup`** (vs. providers-only): `Setup` returns the ready logger for the consistent path; `Logger` remains for manual composition.
- **`Logger` and the LoggerProvider**: `Logger` takes the `LoggerProvider` as an explicit argument (`nil` = console-only) rather than creating one it can't shut down or silently binding to the global. This keeps it lifecycle-free; the caller (or `Setup`) owns provider shutdown.
- **Globals vs `signals.Tracer` helpers**: globals-only (see [instrumentation](#instrumentation-globals-and-the-two-names)).
- **semconv version / env key**: pin v1.41, emit `deployment.environment.name`, bump fleet to Go 1.25, flip collector dimension in lockstep.
