package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/pkg/jwtx"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	userIDKey
)

// RequestIDHeader — заголовок, через который идентификатор запроса передаётся между сервисами.
const RequestIDHeader = "X-Request-ID"

// RequestIDFrom возвращает идентификатор запроса из контекста.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// UserID возвращает идентификатор пользователя, записанный middleware Authenticate.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDKey).(uuid.UUID)
	return id, ok
}

// RequestID берёт X-Request-ID из запроса или создаёт новый и возвращает его в ответе.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" || len(id) > 64 {
			id = uuid.NewString()
		}

		w.Header().Set(RequestIDHeader, id)

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// Recover превращает панику обработчика в 500 и пишет стек в лог.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					handlePanic(log, w, r, p)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

func handlePanic(
	log *slog.Logger,
	w http.ResponseWriter,
	r *http.Request,
	v any,
) {
	log.ErrorContext(
		r.Context(),
		"panic in handler",
		"panic", v,
		"stack", string(debug.Stack()),
		"request_id", RequestIDFrom(r.Context()),
	)
	WriteProblem(w, r, http.StatusInternalServerError, "internal", "")
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// AccessLog пишет по одной строке на запрос: метод, путь, статус, длительность.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{
				ResponseWriter: w,
				status:         http.StatusOK,
			}

			next.ServeHTTP(sw, r)

			log.Info(
				"request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"latency_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFrom(r.Context()),
			)
		})
	}
}

// Authenticate требует корректный Bearer-токен и кладёт id пользователя в контекст. Иначе отвечает 401.
func Authenticate(v jwtx.Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
				w.Header().Set("WWW-Authenticate", "Bearer")
				WriteProblem(w, r, http.StatusUnauthorized, "unauthorized", "missing or malformed bearer token")
				return
			}

			claims, err := v.Verify(strings.TrimSpace(token))
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				WriteProblem(w, r, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userIDKey, claims.UserID)))
		})
	}
}
