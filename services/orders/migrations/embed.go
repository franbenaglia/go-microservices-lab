// Package migrations embebe las migraciones SQL de Orders.
package migrations

import "embed"

// FS contiene los archivos .sql en su raíz.
//
//go:embed *.sql
var FS embed.FS
