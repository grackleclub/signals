package signals

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/resource"
)

// TestNewResource_Environment pins the deployment.environment.name precedence:
// Config wins over env, env wins over the "unknown" default.
func TestNewResource_Environment(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults to unknown", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
		res, err := newResource(ctx, Config{Service: "svc"})
		if err != nil {
			t.Fatal(err)
		}
		if got := envOf(res); got != "unknown" {
			t.Errorf("got %q, want unknown", got)
		}
	})

	t.Run("config wins", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=staging")
		res, err := newResource(ctx, Config{Service: "svc", Env: "prod"})
		if err != nil {
			t.Fatal(err)
		}
		if got := envOf(res); got != "prod" {
			t.Errorf("got %q, want prod", got)
		}
	})

	t.Run("env beats the default", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=staging")
		res, err := newResource(ctx, Config{Service: "svc"})
		if err != nil {
			t.Fatal(err)
		}
		if got := envOf(res); got != "staging" {
			t.Errorf("got %q, want staging", got)
		}
	})
}

func envOf(res *resource.Resource) string {
	v, _ := res.Set().Value(keyDeployEnv)
	return v.AsString()
}
