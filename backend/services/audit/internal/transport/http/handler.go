// Package transporthttp содержит HTTP-слой сервиса audit.
package transporthttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"ovk-system-hotel/pkg/httpx"
	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/services/audit/internal/domain"
	"ovk-system-hotel/services/audit/internal/service"
)

// Journal описывает используемую часть сервиса.
type Journal interface {
	List(ctx context.Context, entityID *uuid.UUID, limit int, cursor string) (service.Page, error)
}

// Pinger проверяет готовность зависимостей.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Handler struct {
	journal Journal
	ready   Pinger
	log     *slog.Logger
}

func NewRouter(
	log *slog.Logger,
	journal Journal,
	ready Pinger,
	verifier jwtx.Verifier,
) http.Handler {
	h := &Handler{
		journal: journal,
		ready:   ready,
		log:     log,
	}

	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.Recover(log), httpx.AccessLog(log))

	r.Get("/healthz", h.healthz)
	r.Get("/readyz", h.readyz)

	r.With(httpx.Authenticate(verifier)).Get("/api/audit", h.list)

	return r
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.ready.Ping(ctx); err != nil {
		h.log.WarnContext(ctx, "readiness check failed", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := service.DefaultLimit
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > service.MaxLimit {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", "limit must be an integer between 1 and "+strconv.Itoa(service.MaxLimit))
			return
		}
		limit = n
	}

	var entityID *uuid.UUID
	if s := q.Get("entity_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", "entity_id must be a valid UUID")
			return
		}
		entityID = &id
	}

	page, err := h.journal.List(r.Context(), entityID, limit, q.Get("cursor"))
	switch {
	case errors.Is(err, domain.ErrInvalidCursor):
		httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", "cursor is invalid")

	case err != nil:
		h.log.ErrorContext(r.Context(), "list audit failed", "error", err, "request_id", httpx.RequestIDFrom(r.Context()))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "")

	default:
		httpx.WriteJSON(w, http.StatusOK, toPageResponse(page))
	}
}
