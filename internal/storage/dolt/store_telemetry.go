// Package dolt implements the storage interface using Dolt (versioned MySQL-compatible database).
//
// Dolt provides native version control for SQL data with cell-level merge, history queries,
// and federation via Dolt remotes. The database itself is version-controlled.
//
// Dolt capabilities:
//   - Native version control (commit, push, pull, branch, merge)
//   - Time-travel queries via AS OF and dolt_history_* tables
//   - Cell-level merge for conflict resolution
//   - Multi-writer via dolt sql-server (federation, pure Go)
//
// All operations require a running dolt sql-server. Connect via MySQL protocol (pure Go).
package dolt

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// doltTracer is the OTel tracer for SQL-level spans.
// It uses the global provider, which is a no-op until telemetry.Init() is called.
var doltTracer = otel.Tracer("github.com/steveyegge/beads/storage/dolt")

// doltMetrics holds OTel metric instruments for the dolt storage backend.
// Instruments are registered against the global delegating provider at init time,
// so they automatically forward to the real provider once telemetry.Init() runs.
var doltMetrics struct {
	retryCount          metric.Int64Counter
	lockWaitMs          metric.Float64Histogram
	circuitTrips        metric.Int64Counter
	circuitRejected     metric.Int64Counter
	serializationErrors metric.Int64Counter
	writeRetries        metric.Int64Counter
	connAcquireMs       metric.Float64Histogram
	poolWaitCount       metric.Int64Counter
	poolWaitMs          metric.Float64Histogram
}

func init() {
	m := otel.Meter("github.com/steveyegge/beads/storage/dolt")
	doltMetrics.retryCount, _ = m.Int64Counter("bd.db.retry_count",
		metric.WithDescription("SQL operations retried due to server-mode transient errors"),
		metric.WithUnit("{retry}"),
	)
	doltMetrics.lockWaitMs, _ = m.Float64Histogram("bd.db.lock_wait_ms",
		metric.WithDescription("Time spent waiting to acquire database locks"),
		metric.WithUnit("ms"),
	)
	doltMetrics.circuitTrips, _ = m.Int64Counter("bd.db.circuit_trips",
		metric.WithDescription("Number of times the Dolt circuit breaker tripped open"),
		metric.WithUnit("{trip}"),
	)
	doltMetrics.circuitRejected, _ = m.Int64Counter("bd.db.circuit_rejected",
		metric.WithDescription("Requests rejected by open circuit breaker (fail-fast)"),
		metric.WithUnit("{request}"),
	)
	doltMetrics.serializationErrors, _ = m.Int64Counter("bd.db.serialization_errors",
		metric.WithDescription("Serialization failures (MySQL 1213/1205) before retry"),
		metric.WithUnit("{error}"),
	)
	doltMetrics.writeRetries, _ = m.Int64Counter("bd.write_retries_total",
		metric.WithDescription("Write-tx retries in withRetryTx (label: type=serialization|connection)"),
		metric.WithUnit("{retry}"),
	)
	doltMetrics.connAcquireMs, _ = m.Float64Histogram("bd.db.conn_acquire_ms",
		metric.WithDescription("Time to acquire a pooled connection for a Dolt transaction"),
		metric.WithUnit("ms"),
	)
	doltMetrics.poolWaitCount, _ = m.Int64Counter("bd.db.pool_wait_count",
		metric.WithDescription("Number of times a connection acquisition had to wait for the pool"),
		metric.WithUnit("{wait}"),
	)
	doltMetrics.poolWaitMs, _ = m.Float64Histogram("bd.db.pool_wait_ms",
		metric.WithDescription("Total time connections spent waiting due to pool exhaustion"),
		metric.WithUnit("ms"),
	)
}

// registerPoolGauges registers observable gauges that report sql.DB pool stats
// on each OTel collection cycle. These are essential for diagnosing shared-server
// degradation under multi-worktree load (GH#3140).
func (s *DoltStore) registerPoolGauges() {
	m := otel.Meter("github.com/steveyegge/beads/storage/dolt")
	db := s.db

	m.Int64ObservableGauge("bd.db.pool_open", //nolint:errcheck,gosec
		metric.WithDescription("Current number of open connections (in-use + idle)"),
		metric.WithUnit("{connection}"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(db.Stats().OpenConnections))
			return nil
		}),
	)
	m.Int64ObservableGauge("bd.db.pool_in_use", //nolint:errcheck,gosec
		metric.WithDescription("Connections currently in use"),
		metric.WithUnit("{connection}"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(db.Stats().InUse))
			return nil
		}),
	)
	m.Int64ObservableGauge("bd.db.pool_idle", //nolint:errcheck,gosec
		metric.WithDescription("Idle connections in pool"),
		metric.WithUnit("{connection}"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(db.Stats().Idle))
			return nil
		}),
	)
	m.Int64ObservableGauge("bd.db.pool_max_open", //nolint:errcheck,gosec
		metric.WithDescription("Maximum number of open connections (pool limit)"),
		metric.WithUnit("{connection}"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(db.Stats().MaxOpenConnections))
			return nil
		}),
	)
}

// doltSpanAttrs returns the fixed attributes shared by all SQL spans.
// Cached to avoid allocating on every call (hot path when telemetry is disabled
// still flows through no-op tracers).
func (s *DoltStore) doltSpanAttrs() []attribute.KeyValue {
	s.spanAttrsOnce.Do(func() {
		s.spanAttrsCache = []attribute.KeyValue{
			attribute.String("db.system", "dolt"),
			attribute.Bool("db.readonly", s.readOnly),
			attribute.Bool("db.server_mode", true), // TODO: update when embedded mode returns
		}
	})
	return s.spanAttrsCache
}

// spanSQL truncates a SQL string to keep spans readable.
func spanSQL(q string) string {
	if len(q) > 300 {
		return q[:300] + "…"
	}
	return q
}

// endSpan records an error (if any) and ends the span.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
