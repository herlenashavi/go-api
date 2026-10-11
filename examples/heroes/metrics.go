package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
)

// instrumentedHeroStore wraps a heroStore with the custom business metrics
// this example defines on top of the framework's own HTTP/DB metrics — see
// docs/metrics.md for the general pattern. Wrapping the Store, rather than
// reaching into crud.Resource, is deliberate: pkg/crud has no
// Create/Update/Delete lifecycle hooks (see its package doc), and a Store
// decorator is the idiomatic place for any cross-cutting behavior layered
// on an existing one — the same shape you'd reach for to add caching or
// audit logging in front of a store.
type instrumentedHeroStore struct {
	heroStore
	created metric.Int64Counter
	retired metric.Int64Counter
}

// newInstrumentedHeroStore builds the counters once — never inside a
// request path, the same rule telemetry.NewHTTPMetrics follows for the
// framework's own instruments — and wraps next with them. meter is
// application.Telemetry.Meter: the exact Meter backing the framework's
// http.server.* metrics, so these show up at /health/metrics (and via
// OTLP, if configured) with no separate registration step.
func newInstrumentedHeroStore(meter metric.Meter, next heroStore) (heroStore, error) {
	created, err := meter.Int64Counter(
		"heroes.created",
		metric.WithDescription("Count of heroes created."),
	)
	if err != nil {
		return nil, fmt.Errorf("heroes.created counter: %w", err)
	}

	retired, err := meter.Int64Counter(
		"heroes.retired",
		metric.WithDescription("Count of heroes retired."),
	)
	if err != nil {
		return nil, fmt.Errorf("heroes.retired counter: %w", err)
	}

	return &instrumentedHeroStore{heroStore: next, created: created, retired: retired}, nil
}

func (s *instrumentedHeroStore) Create(ctx context.Context, in CreateHero) (Hero, error) {
	hero, err := s.heroStore.Create(ctx, in)
	if err == nil {
		s.created.Add(ctx, 1)
	}
	return hero, err
}

func (s *instrumentedHeroStore) Retire(ctx context.Context, id uuid.UUID) (Hero, error) {
	hero, err := s.heroStore.Retire(ctx, id)
	if err == nil {
		s.retired.Add(ctx, 1)
	}
	return hero, err
}
