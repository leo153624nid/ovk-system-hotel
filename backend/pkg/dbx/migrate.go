// Package dbx содержит общий код работы с PostgreSQL.
package dbx

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // драйвер "pgx" для database/sql
	"github.com/pressly/goose/v3"
)

// Migrate применяет все ещё не применённые миграции из fsys (SQL-файлы goose в корне fsys).
func Migrate(ctx context.Context, log *slog.Logger, dbURL string, fsys fs.FS) error {
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return err
	}

	results, err := provider.Up(ctx)
	if err != nil && !errors.Is(err, goose.ErrNoMigrations) {
		return err
	}

	for _, r := range results {
		log.Info("migration applied", "version", r.Source.Version, "duration", r.Duration.String())
	}
	log.Info("migrations up to date")

	return nil
}
