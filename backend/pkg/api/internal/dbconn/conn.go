// Package dbconn owns the database connection shared by the api stack.
package dbconn

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// PoolConfig holds the connection pool limits applied when the connection is opened.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Conn owns the database connection shared by the api stack.
type Conn struct {
	db *sqlx.DB
}

// Open opens the database connection, verifies it is reachable and applies the
func Open(driver string, url string, pool PoolConfig, profiling ProfilingConfig) (*Conn, error) {
	if profiling.Enabled {
		if err := registerProfilingMetrics(); err != nil {
			return nil, fmt.Errorf("cannot register query profiling metrics: %w", err)
		}
		SetSlowQueryThreshold(profiling.SlowQueryThreshold)

		profiledDriver, err := profilingDriverName(driver)
		if err != nil {
			return nil, err
		}
		driver = profiledDriver
	}

	db, err := sqlx.Open(driver, url)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(pool.MaxOpenConns)
	db.SetMaxIdleConns(pool.MaxIdleConns)
	db.SetConnMaxLifetime(pool.ConnMaxLifetime)

	return &Conn{db: db}, nil
}

// Close releases the connection.
func (c *Conn) Close() error {
	return c.db.Close()
}

// DB returns the underlying database handle. It is a package function rather
// than a method so the handle stays unreachable outside pkg/api.
func DB(c *Conn) *sqlx.DB {
	return c.db
}
