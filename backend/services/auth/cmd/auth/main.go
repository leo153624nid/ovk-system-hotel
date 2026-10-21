package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // драйвер "pgx" для database/sql (миграции)
	"github.com/pressly/goose/v3"

	"ovk-system-hotel/pkg/config"
	"ovk-system-hotel/pkg/httpx"
	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/pkg/logx"
	"ovk-system-hotel/services/auth/internal/repository"
	"ovk-system-hotel/services/auth/internal/service"
	"ovk-system-hotel/services/auth/internal/transport"
	"ovk-system-hotel/services/auth/migrations"
)

const serviceName = "auth"

func main() {
	log := logx.New(serviceName, config.String("LOG_LEVEL", "info"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		err = migrate(ctx, log)
	} else {
		err = serve(ctx, log)
	}

	if err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, log *slog.Logger) error {
	dbURL, err := config.Required("DATABASE_URL")
	if err != nil {
		return err
	}

	secret, err := config.Required("JWT_SECRET")
	if err != nil {
		return err
	}

	signer, err := jwtx.NewHS256(secret, config.String("JWT_ISSUER", "ovk-auth"))
	if err != nil {
		return err
	}

	accessTTL, err := config.Duration("ACCESS_TOKEN_TTL", 15*time.Minute)
	if err != nil {
		return err
	}

	refreshTTL, err := config.Duration("REFRESH_TOKEN_TTL", 7*24*time.Hour)
	if err != nil {
		return err
	}

	cost, err := config.Int("BCRYPT_COST", 12)
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	repo := repository.New(pool)
	svc, err := service.New(
		repo,
		signer,
		service.Config{
			AccessTTL:  accessTTL,
			RefreshTTL: refreshTTL,
			BcryptCost: cost,
		},
	)
	if err != nil {
		return err
	}

	secure := config.String("COOKIE_SECURE", "false") == "true"
	router := transport.NewRouter(log, svc, repo, secure)

	return httpx.Run(ctx, log, config.String("HTTP_ADDR", ":8081"), router)
}

func migrate(ctx context.Context, log *slog.Logger) error {
	dbURL, err := config.Required("DATABASE_URL")
	if err != nil {
		return err
	}

	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
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
