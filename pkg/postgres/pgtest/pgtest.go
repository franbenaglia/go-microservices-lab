// Package pgtest ayuda a las pruebas de integración: crea un schema
// PostgreSQL aislado por test, aplica las migraciones y lo elimina al terminar.
//
// Las pruebas se omiten si no está definida LAB_TEST_PG_DSN, por ejemplo:
//
//	LAB_TEST_PG_DSN=postgres://lab:lab@localhost:55432/labtest?sslmode=disable
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/franbenaglia/go-microservices-lab/pkg/postgres"
)

// EnvDSN es la variable con el DSN de la base de pruebas.
const EnvDSN = "LAB_TEST_PG_DSN"

// NewPool devuelve un pool sobre un schema nuevo con las migraciones aplicadas.
func NewPool(t testing.TB, migrations fs.FS) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		t.Skipf("%s no definida: se omite la prueba de integración", EnvDSN)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "t_" + hex.EncodeToString(b)

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("crear schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})
	if err := postgres.Migrate(ctx, pool, migrations); err != nil {
		t.Fatalf("migrar: %v", err)
	}
	return pool
}
