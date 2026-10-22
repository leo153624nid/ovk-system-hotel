// Package proxy создаёт reverse proxy к внутренним сервисам.
package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"ovk-system-hotel/pkg/httpx"
)

const userIDHeader = "X-User-ID"

// New возвращает обработчик, который пересылает запрос на target без изменения пути.
// clientIP определяет адрес клиента для заголовка X-Forwarded-For.
func New(log *slog.Logger, target *url.URL, clientIP func(*http.Request) string) http.Handler {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}

	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// Заголовок личности выставляет только gateway, значение от клиента никогда не пропускаем.
			r.Out.Header.Del(userIDHeader)
			if id, ok := httpx.UserID(r.In.Context()); ok {
				r.Out.Header.Set(userIDHeader, id.String())
			}
			r.Out.Header.Set(httpx.RequestIDHeader, httpx.RequestIDFrom(r.In.Context()))
			r.Out.Header.Set("X-Forwarded-For", clientIP(r.In))
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return // клиент отключился
			}
			log.ErrorContext(r.Context(), "upstream error", "target", target.Host, "path", r.URL.Path, "error", err,
				"request_id", httpx.RequestIDFrom(r.Context()))
			httpx.WriteProblem(w, r, http.StatusBadGateway, "bad_gateway", "upstream service is unavailable")
		},
	}
}
