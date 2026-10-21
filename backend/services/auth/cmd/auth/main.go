package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"ovk-system-hotel/pkg/config"
	"ovk-system-hotel/pkg/httpx"
	"ovk-system-hotel/pkg/logx"
)

const serviceName = "auth"

func main() {
	log := logx.New(serviceName, config.String("LOG_LEVEL", "info"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	httpx.Health(mux)

	addr := config.String("HTTP_ADDR", ":8081")
	if err := httpx.Run(ctx, log, addr, mux); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
