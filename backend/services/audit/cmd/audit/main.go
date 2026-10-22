package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"

	"ovk-system-hotel/pkg/config"
	"ovk-system-hotel/pkg/dbx"
	"ovk-system-hotel/pkg/httpx"
	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/pkg/logx"
	"ovk-system-hotel/pkg/natsx"
	"ovk-system-hotel/services/audit/internal/consumer"
	"ovk-system-hotel/services/audit/internal/repository"
	"ovk-system-hotel/services/audit/internal/service"
	transporthttp "ovk-system-hotel/services/audit/internal/transport/http"
	"ovk-system-hotel/services/audit/migrations"
)

const (
	serviceName  = "audit"
	consumerName = "audit"
)

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

func migrate(ctx context.Context, log *slog.Logger) error {
	dbURL, err := config.Required("DATABASE_URL")

	if err != nil {
		return err
	}

	return dbx.Migrate(ctx, log, dbURL, migrations.FS)
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

	natsURL, err := config.Required("NATS_URL")
	if err != nil {
		return err
	}

	verifier, err := jwtx.NewHS256(secret, config.String("JWT_ISSUER", "ovk-auth"))
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	repo := repository.New(pool)
	svc := service.New(repo)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	consumerDone := make(chan error, 1)

	go func() {
		consumerDone <- natsx.RunConsumer(ctx, log, natsx.ConsumerConfig{
			URL:    natsURL,
			Name:   serviceName,
			Stream: natsx.GuestsStreamConfig(),
			Consumer: jetstream.ConsumerConfig{
				Durable:       consumerName,
				AckPolicy:     jetstream.AckExplicitPolicy,
				AckWait:       30 * time.Second,
				DeliverPolicy: jetstream.DeliverAllPolicy,
				FilterSubject: natsx.GuestsStreamConfig().Subjects[0],
			},
		}, consumer.Handler(svc))
	}()

	router := transporthttp.NewRouter(log, svc, repo, verifier)
	err = httpx.Run(ctx, log, config.String("HTTP_ADDR", ":8083"), router)

	cancel()

	if cerr := <-consumerDone; err == nil {
		err = cerr
	}

	return err
}
