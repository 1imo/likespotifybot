package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const dbPingTimeout = 5 * time.Second

// DB is the database facade. Reads take cacheLifetime: 0 = always hit DB; >0 uses in-memory TTL until invalidation on Exec.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, cacheLifetime time.Duration, args ...any) (Rows, error)
	QueryRowContext(ctx context.Context, query string, cacheLifetime time.Duration, args ...any) Row
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Database wraps *sql.DB with optional read caching.
type Database struct {
	raw   *sql.DB
	cache *readQueryCache
}

func NewDatabase(raw *sql.DB) *Database {
	return &Database{raw: raw, cache: newReadQueryCache()}
}

func (d *Database) SQL() *sql.DB {
	return d.raw
}

func (d *Database) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := d.raw.ExecContext(ctx, query, args...)
	if err == nil && d.cache != nil {
		d.cache.invalidate()
	}
	return res, err
}

func (d *Database) QueryContext(ctx context.Context, query string, cacheLifetime time.Duration, args ...any) (Rows, error) {
	if d.cache == nil || cacheLifetime <= 0 {
		rows, err := d.raw.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		return rows, nil
	}
	key := d.cache.keyQuery(query, args)
	if cols, data, ok := d.cache.getRows(key); ok {
		return newMaterializedRows(cols, data), nil
	}
	rows, err := d.raw.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, data, err := drainSQLRows(rows)
	if err != nil {
		return nil, err
	}
	d.cache.setRows(key, cols, data, cacheLifetime)
	return newMaterializedRows(cols, cloneRowsData(data)), nil
}

func (d *Database) QueryRowContext(ctx context.Context, query string, cacheLifetime time.Duration, args ...any) Row {
	if d.cache == nil || cacheLifetime <= 0 {
		return d.raw.QueryRowContext(ctx, query, args...)
	}
	key := d.cache.keyQuery(query, args)
	if vals, err, ok := d.cache.getRow(key); ok {
		if err != nil {
			return &materializedRow{err: err}
		}
		return &materializedRow{vals: vals}
	}
	rows, err := d.raw.QueryContext(ctx, query, args...)
	if err != nil {
		return &materializedRow{err: err}
	}
	defer rows.Close()
	cols, data, err := drainSQLRows(rows)
	if err != nil {
		return &materializedRow{err: err}
	}
	_ = cols
	if len(data) == 0 {
		// Do not cache misses (sql.ErrNoRows). Missing rows can be created moments later,
		// and caching that miss can cause stale "not found" behavior.
		return &materializedRow{err: sql.ErrNoRows}
	}
	vals := data[0]
	d.cache.setRowOK(key, vals, cacheLifetime)
	return &materializedRow{vals: vals}
}

func (d *Database) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return d.raw.BeginTx(ctx, opts)
}

func (d *Database) Close() error {
	return d.raw.Close()
}

type DatabaseFactory struct{}

func NewDatabaseFactory() *DatabaseFactory {
	return &DatabaseFactory{}
}

func (f *DatabaseFactory) Init(ctx context.Context) (*Database, error) {
	return OpenDatabaseFromEnv(ctx)
}

func OpenDatabaseFromEnv(ctx context.Context) (*Database, error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	// Supabase pooler (PgBouncer transaction mode) does not support prepared statements across
	// pooled connections; lib/pq + database/sql triggers "unnamed prepared statement does not exist".
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	raw := stdlib.OpenDB(*cfg)

	pingCtx, cancel := context.WithTimeout(ctx, dbPingTimeout)
	defer cancel()
	if err := raw.PingContext(pingCtx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return NewDatabase(raw), nil
}
