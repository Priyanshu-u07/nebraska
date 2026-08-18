package dbconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/flatcar/nebraska/backend/pkg/logger"
)

const defaultSlowQueryThreshold = 250 * time.Millisecond

var pl = logger.New("dbprofiling")

type ProfilingConfig struct {
	Enabled            bool
	SlowQueryThreshold time.Duration
}

var (
	slowQueryThresholdNanos atomic.Int64

	queryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "nebraska",
			Name:      "db_query_duration_seconds",
			Help:      "Duration of database queries by operation",
			Buckets:   []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"operation"},
	)

	slowQueries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "nebraska",
			Name:      "db_slow_queries_total",
			Help:      "Number of database queries slower than the configured threshold",
		},
		[]string{"operation"},
	)

	queryErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "nebraska",
			Name:      "db_query_errors_total",
			Help:      "Number of database queries that returned an error",
		},
		[]string{"operation"},
	)

	registerProfilingOnce sync.Once
	registerProfilingErr  error

	registeredDrivers   = map[string]string{}
	registeredDriversMu sync.Mutex
)

func SetSlowQueryThreshold(d time.Duration) {
	if d <= 0 {
		d = defaultSlowQueryThreshold
	}
	slowQueryThresholdNanos.Store(int64(d))
}

func slowQueryThreshold() time.Duration {
	return time.Duration(slowQueryThresholdNanos.Load())
}

func profilingDriverName(baseDriver string) (string, error) {
	registeredDriversMu.Lock()
	defer registeredDriversMu.Unlock()

	if name, ok := registeredDrivers[baseDriver]; ok {
		return name, nil
	}

	probe, err := sql.Open(baseDriver, "")
	if err != nil {
		return "", fmt.Errorf("cannot resolve driver %q: %w", baseDriver, err)
	}
	base := probe.Driver()
	if err := probe.Close(); err != nil {
		return "", fmt.Errorf("cannot close driver probe for %q: %w", baseDriver, err)
	}

	name := baseDriver + "-profiled"
	sql.Register(name, profilingDriver{base: base})
	registeredDrivers[baseDriver] = name

	return name, nil
}

func registerProfilingMetrics() error {
	registerProfilingOnce.Do(func() {
		for _, c := range []prometheus.Collector{queryDuration, slowQueries, queryErrors} {
			if err := prometheus.Register(c); err != nil {
				registerProfilingErr = err
				return
			}
		}
	})
	return registerProfilingErr
}

func observe(query string, start time.Time, err error) {
	op := classify(query)
	elapsed := time.Since(start)

	queryDuration.WithLabelValues(op).Observe(elapsed.Seconds())

	if err != nil && err != driver.ErrSkip {
		queryErrors.WithLabelValues(op).Inc()
	}

	if threshold := slowQueryThreshold(); threshold > 0 && elapsed >= threshold {
		slowQueries.WithLabelValues(op).Inc()
		pl.Warn().
			Str("operation", op).
			Dur("duration", elapsed).
			Str("query", collapseWhitespace(query)).
			Msg("slow query")
	}
}

func classify(query string) string {
	keyword, _, _ := strings.Cut(strings.TrimLeft(query, " \t\r\n("), " ")

	switch strings.ToLower(strings.TrimRight(keyword, "\t\r\n")) {
	case "select":
		return "select"
	case "insert":
		return "insert"
	case "update":
		return "update"
	case "delete":
		return "delete"
	case "with":
		return "select"
	default:
		return "other"
	}
}

func collapseWhitespace(query string) string {
	return strings.Join(strings.Fields(query), " ")
}

type profilingDriver struct{ base driver.Driver }

func (d profilingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &profilingConn{base: conn}, nil
}

type profilingConn struct{ base driver.Conn }

func (c *profilingConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.base.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &profilingStmt{base: stmt, query: query}, nil
}

func (c *profilingConn) Close() error { return c.base.Close() }

//nolint:staticcheck // driver.Conn requires Begin; ConnBeginTx is provided below.
func (c *profilingConn) Begin() (driver.Tx, error) { return c.base.Begin() }

func (c *profilingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	preparer, ok := c.base.(driver.ConnPrepareContext)
	if !ok {
		return c.Prepare(query)
	}
	stmt, err := preparer.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &profilingStmt{base: stmt, query: query}, nil
}

func (c *profilingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.base.(driver.ConnBeginTx)
	if !ok {
		//nolint:staticcheck // fallback for drivers without ConnBeginTx.
		return c.base.Begin()
	}
	return beginner.BeginTx(ctx, opts)
}

func (c *profilingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.base.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	rows, err := queryer.QueryContext(ctx, query, args)
	observe(query, start, err)
	return rows, err
}

func (c *profilingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.base.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	res, err := execer.ExecContext(ctx, query, args)
	observe(query, start, err)
	return res, err
}

func (c *profilingConn) Ping(ctx context.Context) error {
	pinger, ok := c.base.(driver.Pinger)
	if !ok {
		return nil
	}
	return pinger.Ping(ctx)
}

func (c *profilingConn) ResetSession(ctx context.Context) error {
	resetter, ok := c.base.(driver.SessionResetter)
	if !ok {
		return nil
	}
	return resetter.ResetSession(ctx)
}

func (c *profilingConn) IsValid() bool {
	validator, ok := c.base.(driver.Validator)
	if !ok {
		return true
	}
	return validator.IsValid()
}

type profilingStmt struct {
	base  driver.Stmt
	query string
}

func (s *profilingStmt) Close() error  { return s.base.Close() }
func (s *profilingStmt) NumInput() int { return s.base.NumInput() }

//nolint:staticcheck // driver.Stmt requires Exec; ExecContext is provided below.
func (s *profilingStmt) Exec(args []driver.Value) (driver.Result, error) {
	start := time.Now()
	res, err := s.base.Exec(args)
	observe(s.query, start, err)
	return res, err
}

//nolint:staticcheck // driver.Stmt requires Query; QueryContext is provided below.
func (s *profilingStmt) Query(args []driver.Value) (driver.Rows, error) {
	start := time.Now()
	rows, err := s.base.Query(args)
	observe(s.query, start, err)
	return rows, err
}

func (s *profilingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := s.base.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	res, err := execer.ExecContext(ctx, args)
	observe(s.query, start, err)
	return res, err
}

func (s *profilingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := s.base.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	rows, err := queryer.QueryContext(ctx, args)
	observe(s.query, start, err)
	return rows, err
}
