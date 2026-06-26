//go:build integration

package signals_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/grackleclub/signals"
	"go.opentelemetry.io/otel"
)

// scope is this test's instrumentation scope (convention: import path).
const scope = "github.com/grackleclub/signals/itest"

// TestIntegration_Signals is the full roundtrip: Setup exports to a running
// otelcol-contrib collector over OTLP/HTTP, we emit a trace, a correlated log,
// and a metric, flush, then read the collector's file-exporter output and
// assert all three arrived sharing one identity.
//
// Requires a collector (./bin/test up). Env:
//   - OTEL_EXPORTER_OTLP_ENDPOINT  (default http://localhost:4318)
//   - SIGNALS_COLLECTOR_OUT        (default ./collector/out/signals.json)
//
// Fails today because Setup is stubbed; it is the executable spec for the
// implementation.
func TestIntegration_Signals(t *testing.T) {
	endpoint := env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	outPath := env("SIGNALS_COLLECTOR_OUT", "collector/out/signals.json")

	// Unique per run so assertions ignore any leftover lines in the file.
	run := fmt.Sprintf("itest-%d", time.Now().UnixNano())
	service := "signals-" + run
	spanName := "span-" + run
	logBody := "log-" + run
	metricName := "events." + run

	ctx := context.Background()
	shutdown, log, err := signals.Setup(ctx, signals.Config{
		Env:      "test",
		Service:  service,
		Version:  "itest",
		Endpoint: endpoint,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Trace + correlated log (logged with ctx, inside the span) + metric.
	tracer := otel.Tracer(scope)
	ctx, span := tracer.Start(ctx, spanName)
	wantTrace := span.SpanContext().TraceID().String()
	wantSpan := span.SpanContext().SpanID().String()

	log.InfoContext(ctx, logBody, "run", run)

	counter, err := otel.Meter(scope).Int64Counter(metricName)
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	counter.Add(ctx, 1)

	span.End()

	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown (flush): %v", err)
	}

	// The collector writes asynchronously; poll the file until our run shows up.
	dump := waitForDump(t, outPath, run, 15*time.Second)

	// --- traces ---
	sp, ok := dump.findSpan(service, spanName)
	if !ok {
		t.Fatalf("no span %q for service %q in collector output", spanName, service)
	}
	if sp.TraceID != wantTrace {
		t.Errorf("span traceId = %s, want %s", sp.TraceID, wantTrace)
	}

	// --- metrics ---
	if !dump.hasMetric(service, metricName) {
		t.Errorf("no metric %q for service %q in collector output", metricName, service)
	}

	// --- logs + correlation contract ---
	lr, ok := dump.findLog(service, logBody)
	if !ok {
		t.Fatalf("no log %q for service %q in collector output", logBody, service)
	}
	if lr.TraceID != wantTrace {
		t.Errorf("log traceId = %s, want %s (logs not correlated to span)", lr.TraceID, wantTrace)
	}
	if lr.SpanID != wantSpan {
		t.Errorf("log spanId = %s, want %s", lr.SpanID, wantSpan)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// waitForDump polls outPath until a line referencing run is present, then
// returns the accumulated parse of the whole file.
func waitForDump(t *testing.T, outPath, run string, timeout time.Duration) collectorDump {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		dump, raw, err := readDump(outPath)
		if err == nil && strings.Contains(raw, run) {
			return dump
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("reading collector output %q: %v", outPath, err)
			}
			t.Fatalf("timed out after %s waiting for run %q in %q", timeout, run, outPath)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// --- minimal OTLP/JSON decoding (file exporter, pdata JSON encoding) ---
//
// We decode only the fields the assertions need. traceId/spanId are lowercase
// hex in pdata's JSON marshaler, matching SpanContext().*.String().

type collectorDump struct {
	spans   []spanRecord
	metrics []metricRecord
	logs    []logRecord
}

type spanRecord struct {
	service string
	name    string
	TraceID string
	SpanID  string
}

type metricRecord struct {
	service string
	name    string
}

type logRecord struct {
	service string
	body    string
	TraceID string
	SpanID  string
}

func (d collectorDump) findSpan(service, name string) (spanRecord, bool) {
	for _, s := range d.spans {
		if s.service == service && s.name == name {
			return s, true
		}
	}
	return spanRecord{}, false
}

func (d collectorDump) hasMetric(service, name string) bool {
	for _, m := range d.metrics {
		if m.service == service && m.name == name {
			return true
		}
	}
	return false
}

func (d collectorDump) findLog(service, body string) (logRecord, bool) {
	for _, l := range d.logs {
		if l.service == service && l.body == body {
			return l, true
		}
	}
	return logRecord{}, false
}

// otlpLine is one JSON object as written per export by the file exporter.
type otlpLine struct {
	ResourceSpans []struct {
		Resource   otlpResource `json:"resource"`
		ScopeSpans []struct {
			Spans []struct {
				Name    string `json:"name"`
				TraceID string `json:"traceId"`
				SpanID  string `json:"spanId"`
			} `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
	ResourceMetrics []struct {
		Resource     otlpResource `json:"resource"`
		ScopeMetrics []struct {
			Metrics []struct {
				Name string `json:"name"`
			} `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
	ResourceLogs []struct {
		Resource  otlpResource `json:"resource"`
		ScopeLogs []struct {
			LogRecords []struct {
				Body    otlpAnyValue `json:"body"`
				TraceID string       `json:"traceId"`
				SpanID  string       `json:"spanId"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

type otlpResource struct {
	Attributes []otlpAttr `json:"attributes"`
}

type otlpAttr struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue string `json:"stringValue"`
}

func (r otlpResource) service() string {
	for _, a := range r.Attributes {
		if a.Key == "service.name" {
			return a.Value.StringValue
		}
	}
	return ""
}

// readDump parses every JSON line of outPath into a collectorDump and returns
// the raw text (used for the cheap run-id presence poll).
func readDump(path string) (collectorDump, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return collectorDump{}, "", err
	}
	raw := string(b)

	var d collectorDump
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var l otlpLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			// Skip non-JSON / partial lines rather than failing the read.
			continue
		}
		for _, rs := range l.ResourceSpans {
			svc := rs.Resource.service()
			for _, ss := range rs.ScopeSpans {
				for _, s := range ss.Spans {
					d.spans = append(d.spans, spanRecord{
						service: svc, name: s.Name,
						TraceID: s.TraceID, SpanID: s.SpanID,
					})
				}
			}
		}
		for _, rm := range l.ResourceMetrics {
			svc := rm.Resource.service()
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					d.metrics = append(d.metrics, metricRecord{service: svc, name: m.Name})
				}
			}
		}
		for _, rl := range l.ResourceLogs {
			svc := rl.Resource.service()
			for _, sl := range rl.ScopeLogs {
				for _, r := range sl.LogRecords {
					d.logs = append(d.logs, logRecord{
						service: svc, body: r.Body.StringValue,
						TraceID: r.TraceID, SpanID: r.SpanID,
					})
				}
			}
		}
	}
	return d, raw, nil
}
