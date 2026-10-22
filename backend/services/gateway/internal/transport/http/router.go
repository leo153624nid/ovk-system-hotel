// Package transporthttp содержит HTTP-слой gateway: маршрутизацию, авторизацию, лимиты и CORS.
package transporthttp

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"ovk-system-hotel/pkg/httpx"
	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/services/gateway/internal/ratelimit"
)

// Deps — зависимости роутера.
type Deps struct {
	Log      *slog.Logger
	Verifier jwtx.Verifier
	ClientIP func(*http.Request) string

	Auth  http.Handler
	Hotel http.Handler
	Audit http.Handler

	General    *ratelimit.Limiter // на все запросы
	AuthStrict *ratelimit.Limiter // дополнительно на login и register (защита от перебора пароля)

	CORSOrigins []string
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.Recover(d.Log), httpx.AccessLog(d.Log), securityHeaders, cors(d.CORSOrigins))

	// Служебные маршруты без лимитов и авторизации.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	r.Group(func(r chi.Router) {
		r.Use(limit(d.General, d.ClientIP), bodyLimit)

		// auth: публичные маршруты. Токен не нужен, перебор пароля ограничивается строже.
		strict := limit(d.AuthStrict, d.ClientIP)
		r.With(strict).Post("/api/auth/login", d.Auth.ServeHTTP)
		r.With(strict).Post("/api/auth/register", d.Auth.ServeHTTP)
		r.Handle("/api/auth/*", d.Auth)

		// Остальное API только с валидным access-токеном; без него сервисы даже не вызываются.
		r.Group(func(r chi.Router) {
			r.Use(httpx.Authenticate(d.Verifier))
			r.Handle("/api/guests", d.Hotel)
			r.Handle("/api/guests/*", d.Hotel)
			r.Handle("/api/audit", d.Audit)
			r.Handle("/api/audit/*", d.Audit)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "route not found")
	})

	return r
}
