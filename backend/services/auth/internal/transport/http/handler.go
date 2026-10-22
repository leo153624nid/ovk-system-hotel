// Package transporthttp содержит HTTP-слой сервиса auth.
package transporthttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"ovk-system-hotel/pkg/httpx"
	"ovk-system-hotel/services/auth/internal/domain"
	"ovk-system-hotel/services/auth/internal/service"
)

const (
	refreshCookie = "refresh_token"
	cookiePath    = "/api/auth"
)

// Auth описывает используемую часть сервиса.
type Auth interface {
	Register(ctx context.Context, email, password string) (domain.User, error)
	Login(ctx context.Context, email, password string) (service.Tokens, error)
	Refresh(ctx context.Context, rawToken string) (service.Tokens, error)
	Logout(ctx context.Context, rawToken string) error
}

// Pinger проверяет готовность зависимостей.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Handler struct {
	auth         Auth
	ready        Pinger
	log          *slog.Logger
	secureCookie bool
}

func NewRouter(
	log *slog.Logger,
	auth Auth,
	ready Pinger,
	secureCookie bool,
) http.Handler {
	h := &Handler{
		auth:         auth,
		ready:        ready,
		log:          log,
		secureCookie: secureCookie,
	}

	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.Recover(log), httpx.AccessLog(log))

	r.Get("/healthz", h.healthz)
	r.Get("/readyz", h.readyz)

	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", h.register)
		r.Post("/login", h.login)
		r.Post("/refresh", h.refresh)
		r.Post("/logout", h.logout)
	})

	return r
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.ready.Ping(ctx); err != nil {
		h.log.Warn("readiness check failed", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	u, err := h.auth.Register(r.Context(), in.Email, in.Password)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, userResponse{ID: u.ID, Email: u.Email})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	tokens, err := h.auth.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.writeTokens(w, tokens)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(refreshCookie)
	if err != nil {
		h.clearCookie(w)
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "invalid_token", "refresh token is missing")
		return
	}

	tokens, err := h.auth.Refresh(r.Context(), c.Value)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidToken) {
			h.clearCookie(w)
		}

		h.fail(w, r, err)
		return
	}

	h.writeTokens(w, tokens)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var raw string
	if c, err := r.Cookie(refreshCookie); err == nil {
		raw = c.Value
	}

	if err := h.auth.Logout(r.Context(), raw); err != nil {
		h.fail(w, r, err)
		return
	}

	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeTokens(w http.ResponseWriter, t service.Tokens) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure задаётся конфигом: локально сервис работает по http
		Name:     refreshCookie,
		Value:    t.Refresh,
		Path:     cookiePath,
		Expires:  t.RefreshExpires,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, tokenResponse{AccessToken: t.Access, TokenType: "Bearer", ExpiresIn: t.ExpiresIn})
}

func (h *Handler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure задаётся конфигом: локально сервис работает по http
		Name:     refreshCookie,
		Path:     cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

// fail переводит доменную ошибку в HTTP-ответ. Детали внутренних ошибок наружу не отдаются.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *domain.ValidationError

	switch {
	case errors.As(err, &ve):
		fields := make([]httpx.FieldError, len(ve.Fields))
		for i, f := range ve.Fields {
			fields[i] = httpx.FieldError{Field: f.Field, Message: f.Message}
		}
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_failed", "request validation failed", fields...)

	case errors.Is(err, domain.ErrEmailTaken):
		httpx.WriteProblem(w, r, http.StatusConflict, "email_taken", "email is already registered")

	case errors.Is(err, domain.ErrInvalidCredentials):
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")

	case errors.Is(err, domain.ErrInvalidToken):
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "invalid_token", "refresh token is invalid or expired")

	default:
		h.log.Error("request failed", "error", err, "request_id", httpx.RequestIDFrom(r.Context()))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "")
	}
}
