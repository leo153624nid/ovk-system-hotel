package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"ovk-system-hotel/pkg/config"
	"ovk-system-hotel/pkg/httpx"
	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/pkg/logx"
	"ovk-system-hotel/services/gateway/internal/proxy"
	"ovk-system-hotel/services/gateway/internal/ratelimit"
	transporthttp "ovk-system-hotel/services/gateway/internal/transport/http"
)

const (
	serviceName = "gateway"
	limiterIdle = 3 * time.Minute
)

func main() {
	log := logx.New(serviceName, config.String("LOG_LEVEL", "info"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	secret, err := config.Required("JWT_SECRET")
	if err != nil {
		return err
	}

	verifier, err := jwtx.NewHS256(secret, config.String("JWT_ISSUER", "ovk-auth"))
	if err != nil {
		return err
	}

	clientIP, err := transporthttp.NewClientIP(strings.Split(config.String("TRUSTED_PROXIES", ""), ","))
	if err != nil {
		return err
	}

	authH, err := newUpstream(log, clientIP, "AUTH_URL", "http://localhost:8081")
	if err != nil {
		return err
	}

	hotelH, err := newUpstream(log, clientIP, "HOTEL_URL", "http://localhost:8082")
	if err != nil {
		return err
	}

	auditH, err := newUpstream(log, clientIP, "AUDIT_URL", "http://localhost:8083")
	if err != nil {
		return err
	}

	rps, err := config.Int("RATE_LIMIT_RPS", 20)
	if err != nil {
		return err
	}

	burst, err := config.Int("RATE_LIMIT_BURST", 40)
	if err != nil {
		return err
	}

	authPerMin, err := config.Int("AUTH_RATE_LIMIT_PER_MIN", 10)
	if err != nil {
		return err
	}

	authBurst, err := config.Int("AUTH_RATE_LIMIT_BURST", 5)
	if err != nil {
		return err
	}

	if rps < 1 || burst < 1 || authPerMin < 1 || authBurst < 1 {
		return fmt.Errorf("config: rate limit values must be positive")
	}

	general := ratelimit.New(rate.Limit(rps), burst, limiterIdle)
	strict := ratelimit.New(rate.Every(time.Minute/time.Duration(authPerMin)), authBurst, limiterIdle)

	go general.Run(ctx)
	go strict.Run(ctx)

	var origins []string
	for _, o := range strings.Split(config.String("CORS_ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}

	router := transporthttp.NewRouter(transporthttp.Deps{
		Log:         log,
		Verifier:    verifier,
		ClientIP:    clientIP,
		Auth:        authH,
		Hotel:       hotelH,
		Audit:       auditH,
		General:     general,
		AuthStrict:  strict,
		CORSOrigins: origins,
	})

	return httpx.Run(ctx, log, config.String("HTTP_ADDR", ":8080"), router)
}

// newUpstream читает адрес сервиса из переменной env и возвращает proxy к нему.
func newUpstream(log *slog.Logger, clientIP func(*http.Request) string, env, def string) (http.Handler, error) {
	u, err := url.Parse(config.String(env, def))

	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("config: %s must be an absolute URL", env)
	}

	return proxy.New(log, u, clientIP), nil
}
