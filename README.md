# signals

OTel-native observability for grackleclub services — logs, metrics, and traces
from one `Setup`, exported over OTLP/HTTP to SigNoz.

_Replaces the logging role of [grackleclub/log](https://github.com/grackleclub/log)._

## install

```
go get github.com/grackleclub/signals
```

Requires Go 1.25.

## use

```go
func main() {
	ctx := context.Background()
	shutdown, log, err := signals.Setup(ctx, signals.Config{
		Env:     "prod",
		Version: build.SHA,
	})
	if err != nil {
		panic(err)
	}
	defer shutdown(ctx) // flush telemetry on exit

	log.InfoContext(ctx, "starting", "addr", addr) // correlated inside a span
}
```

`Setup` installs the global tracer, meter, and logger providers (sharing one
resource) plus the W3C propagator, and returns a ready `*slog.Logger`. Call it
once from `main`. Libraries don't call `Setup` — they read the globals:

```go
var tracer = otel.Tracer("github.com/grackleclub/rulette/game")
var meter  = otel.Meter("github.com/grackleclub/rulette/game")
```

## config

`Env` is the only required field; the rest fall back to `OTEL_*` env, then
defaults.

| field | purpose |
| --- | --- |
| `Env` | `deployment.environment.name` (primary SigNoz dimension) |
| `Service` | service name; default `OTEL_SERVICE_NAME`, then argv0 |
| `Version` | `service.version` |
| `Endpoint` | OTLP/HTTP URL; default `OTEL_EXPORTER_OTLP_ENDPOINT` |
| `Token` | bearer auth, merged over `OTEL_EXPORTER_OTLP_HEADERS`; default `OTLP_INGEST_TOKEN` |
| `StderrLevel` | console threshold (the OTLP sink ships every level) |
| `DisableRuntimeMetrics` | turn off Go runtime metrics |

## behavior

- **Correlated logs** — a log emitted with a span's context carries
  `trace_id`/`span_id`; the console renders a short `trace_id` inline.
- **Two sinks** — tint console (pretty, `StderrLevel`-gated, `NO_COLOR` aware)
  and OTLP. The OTLP sink is unthresholded — filter in SigNoz.
- **Graceful off** — no endpoint configured ⇒ console only, no error.
- **Traces** — `ParentBased(AlwaysSample)` (override via `OTEL_TRACES_SAMPLER`);
  tail-sampling lives at the collector.
- **Metrics** — periodic OTLP reader plus Go runtime metrics; host metrics are
  the collector's job.

## test

```
bin/test unit      # fast, no docker
bin/test pretty    # eyeball the console output
bin/test ci        # collector roundtrip (docker)
```
