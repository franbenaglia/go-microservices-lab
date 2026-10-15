// Package postgres crea pools de conexiones pgx y aplica migraciones goose
// embebidas en cada servicio.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Options configura el pool.
type Options struct {
	DSN             string
	MaxConns        int32
	ConnectTimeout  time.Duration
	MaxConnLifetime time.Duration
	// ConnectAttempts es el número de intentos de conexión inicial (1 por segundo).
	ConnectAttempts int
}

// NewPool abre un pool y verifica la conexión antes de devolverlo.
func NewPool(ctx context.Context, o Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(o.DSN)
	if err != nil {
		// No se incluye el DSN en el error para no filtrar credenciales.
		return nil, fmt.Errorf("DSN de PostgreSQL inválido")
	}
	if o.MaxConns > 0 {
		cfg.MaxConns = o.MaxConns
	}
	if o.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = o.MaxConnLifetime
	}
	if o.ConnectTimeout > 0 {
		cfg.ConnConfig.ConnectTimeout = o.ConnectTimeout
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("crear pool: %w", err)
	}
	attempts := max(o.ConnectAttempts, 1)
	for i := 1; ; i++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if i >= attempts {
			pool.Close()
			return nil, fmt.Errorf("conectar a PostgreSQL tras %d intentos: %w", i, err)
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Migrate aplica las migraciones pendientes. fsys debe contener los archivos
// .sql de goose en su raíz.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func(db *sql.DB) { _ = db.Close() }(db)

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fmt.Errorf("preparar migraciones: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("aplicar migraciones: %w", err)
	}
	return nil
}
