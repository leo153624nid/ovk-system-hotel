package transporthttp

import (
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"ovk-system-hotel/pkg/httpx"
	"ovk-system-hotel/services/gateway/internal/ratelimit"
)

const maxBodyBytes = 1 << 20

// limit отвечает 429, когда клиент исчерпал свою квоту.
func limit(l *ratelimit.Limiter, clientIP func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ok, retry := l.Allow(clientIP(r)); !ok {
				secs := int(math.Ceil(retry.Round(time.Millisecond).Seconds()))

				w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
				httpx.WriteProblem(w, r, http.StatusTooManyRequests, "rate_limited", "too many requests, retry later")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// bodyLimit отклоняет запросы с заявленным телом больше лимита до обращения к сервисам.
func bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBodyBytes {
			httpx.WriteProblem(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")

		next.ServeHTTP(w, r)
	})
}

// cors разрешает кросс-доменные запросы только с перечисленных origins (cookie refresh-токена требует
// явного origin, а не «*»). Preflight обрабатывается здесь и не доходит ни до авторизации, ни до сервисов.
func cors(allowed []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Add("Vary", "Origin")
			if !slices.Contains(allowed, origin) {
				if isPreflight(r) {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", httpx.RequestIDHeader)
			if isPreflight(r) {
				h.Set("Access-Control-Allow-Methods", strings.Join([]string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete}, ", "))
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+httpx.RequestIDHeader)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}
