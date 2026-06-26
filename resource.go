package signals

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

// newResource builds the Resource shared by all three providers — the join key
// SigNoz uses to correlate logs, metrics, and traces. See DESIGN.md "resource".
//
// service.name and deployment.environment.name use the pinned semconv keys
// (semconv.go); host.name comes from resource.WithHost and OTEL_RESOURCE_-
// ATTRIBUTES / OTEL_SERVICE_NAME from resource.WithFromEnv.
func newResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		keyServiceName.String(cfg.Service),
		keyDeployEnv.String(cfg.Env),
	}
	if cfg.Version != "" {
		attrs = append(attrs, keyServiceVersion.String(cfg.Version))
	}

	res, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithFromEnv(),
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("building resource: %w", err)
	}
	return res, nil
}
