package signals

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

// newResource builds the Resource shared by all three providers — the join key
// SigNoz uses to correlate logs, metrics, and traces. See DESIGN.md "resource".
//
// Stub: the attribute set below pins the shape (service + environment identity
// via the semconv keys); the implementation merges host/env attributes and
// returns a real *resource.Resource.
func newResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	_ = []attribute.KeyValue{
		keyServiceName.String(cfg.Service),
		keyServiceVersion.String(cfg.Version),
		keyDeployEnv.String(cfg.Env),
		keyHostName.String(""),
	}
	return nil, errNotImplemented
}
