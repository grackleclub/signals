# signals

[![test](https://github.com/grackleclub/signals/actions/workflows/test.yml/badge.svg)](https://github.com/grackleclub/signals/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/grackleclub/signals?sort=semver)](https://github.com/grackleclub/signals/releases/latest)

OTel observability: logs, metrics, traces

_(replaces [grackleclub/log](https://github.com/grackleclub/log))_

## install

```
go get github.com/grackleclub/signals
```

_requires go v1.25+_

## use

```go
func main() {
	ctx := context.Background()
	shutdown, log, err := signals.Setup(ctx, signals.Config{
		Env:     "prod",
		Version: build.SHA,
	})
	if err != nil {
		panic(fmt.Errorf("setup signals: %w", err)
	}
	defer shutdown(ctx) // flush telemetry on exit

	log.InfoContext(ctx, "starting", "addr", addr) // contains span details for correlation
}
```

`Setup` installs the global tracer, meter, and logger providers (one shared
resource) plus the W3C propagator, then returns a ready `*slog.Logger`. Call it
once from `main`. Libraries read the globals instead:

```go
var tracer = otel.Tracer("github.com/grackleclub/rulette/game")
var meter  = otel.Meter("github.com/grackleclub/rulette/game")
```

## config

Every field falls back to `OTEL_*` env, then a default.

| field | purpose | default |
| --- | --- | --- |
| `Env` | `deployment.environment.name` | `OTEL_RESOURCE_ATTRIBUTES`, then `unknown` |
| `Service` | `service.name` | `OTEL_SERVICE_NAME`, then argv0 |
| `Version` | `service.version` | none |
| `Endpoint` | OTLP/HTTP URL | `OTEL_EXPORTER_OTLP_ENDPOINT` |
| `Token` | bearer auth, merged over `OTEL_EXPORTER_OTLP_HEADERS` | `OTLP_INGEST_TOKEN` |
| `StderrLevel` | console threshold | INFO |
| `DisableRuntimeMetrics` | drop Go runtime metrics | false |
| `Console` | pterm console tuning (time, caller, compact, width) | time auto (on when captured), one arg per line |

The console is a [pterm](https://github.com/pterm/pterm) logger, so anything in the build that imports `pterm` directly (tables, spinners, boxes) shares signals' styling with no extra wiring. See [examples.md](examples.md).

## behavior

| topic | behavior |
| --- | --- |
| logs | two sinks: pterm console (pretty, `StderrLevel`-gated, `NO_COLOR` aware) and OTLP |
| correlation | a log with a span's context carries `trace_id`/`span_id`; console shows a short `trace_id` |
| levels | console honors `StderrLevel`; OTLP ships every level, filter in SigNoz |
| graceful off | no endpoint configured: console only, no error |
| traces | `ParentBased(AlwaysSample)`, override via `OTEL_TRACES_SAMPLER`; tail-sample at the collector |
| metrics | periodic OTLP reader plus Go runtime metrics; host metrics are the collector's |

## test

| command | does |
| --- | --- |
| `bin/test unit` | fast, no docker |
| `bin/test pretty` | print the colored console output |
| `bin/test ci` | collector roundtrip (docker) |
