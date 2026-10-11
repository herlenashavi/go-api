package dbx

import (
	"context"
	"errors"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestQueryMetricsRecord(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	qm, err := newQueryMetrics(mp.Meter("dbx-test"))
	if err != nil {
		t.Fatalf("newQueryMetrics: %v", err)
	}

	qm.record(context.Background(), 10*time.Millisecond, nil)
	qm.record(context.Background(), 20*time.Millisecond, errors.New("boom"))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var gotCount, gotHistCount int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				if m.Name == "db.client.query.count" {
					for _, dp := range data.DataPoints {
						gotCount += dp.Value
					}
				}
			case metricdata.Histogram[float64]:
				if m.Name == "db.client.query.duration" {
					for _, dp := range data.DataPoints {
						gotHistCount += int64(dp.Count)
					}
				}
			}
		}
	}

	if gotCount != 2 {
		t.Errorf("db.client.query.count = %d, want 2", gotCount)
	}
	if gotHistCount != 2 {
		t.Errorf("db.client.query.duration observation count = %d, want 2", gotHistCount)
	}
}
