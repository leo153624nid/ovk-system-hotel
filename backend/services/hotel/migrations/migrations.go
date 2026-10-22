// Package migrations встраивает SQL-миграции сервиса hotel в бинарник.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
