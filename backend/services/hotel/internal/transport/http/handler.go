// Package transporthttp содержит HTTP-слой сервиса hotel.
package transporthttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"ovk-system-hotel/pkg/httpx"
	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/services/hotel/internal/domain"
	"ovk-system-hotel/services/hotel/internal/service"
)

const maxQueryLen = 100

// Guests описывает используемую часть сервиса.
type Guests interface {
	Create(ctx context.Context, actor uuid.UUID, in service.GuestInput) (domain.Guest, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Guest, error)
	Update(ctx context.Context, actor, id uuid.UUID, in service.GuestInput) (domain.Guest, error)
	Delete(ctx context.Context, actor, id uuid.UUID) error
	List(ctx context.Context, query string, limit int, cursor string) (service.Page, error)
}

// Pinger проверяет готовность зависимостей.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Handler struct {
	guests Guests
	ready  Pinger
	log    *slog.Logger
}

func NewRouter(
	log *slog.Logger,
	guests Guests,
	ready Pinger,
	verifier jwtx.Verifier,
) http.Handler {
	h := &Handler{
		guests: guests,
		ready:  ready,
		log:    log,
	}

	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.Recover(log), httpx.AccessLog(log))

	r.Get("/healthz", h.healthz)
	r.Get("/readyz", h.readyz)

	r.Route("/api/guests", func(r chi.Router) {
		r.Use(httpx.Authenticate(verifier))
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Get("/{id}", h.get)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.delete)
	})

	return r
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

	query := q.Get("q")
	if utf8.RuneCountInString(query) > maxQueryLen {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", "q must be at most "+strconv.Itoa(maxQueryLen)+" characters")
		return
	}

	page, err := h.guests.List(r.Context(), query, limit, q.Get("cursor"))
	if err != nil {
		h.fail(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toPageResponse(page))
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	actor, _ := httpx.UserID(r.Context())
	var in guestRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	g, err := h.guests.Create(r.Context(), actor, in.toInput())
	if err != nil {
		h.fail(w, r, err)
		return
	}

	w.Header().Set("Location", "/api/guests/"+g.ID.String())
	httpx.WriteJSON(w, http.StatusCreated, toGuestResponse(g))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}

	g, err := h.guests.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toGuestResponse(g))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}

	actor, _ := httpx.UserID(r.Context())
	var in guestRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	g, err := h.guests.Update(r.Context(), actor, id, in.toInput())
	if err != nil {
		h.fail(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toGuestResponse(g))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}

	actor, _ := httpx.UserID(r.Context())
	if err := h.guests.Delete(r.Context(), actor, id); err != nil {
		h.fail(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", "id must be a valid UUID")
		return uuid.Nil, false
	}

	return id, true
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

	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "guest not found")

	case errors.Is(err, domain.ErrInvalidCursor):
		httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", "cursor is invalid")

	default:
		h.log.Error("request failed", "error", err, "request_id", httpx.RequestIDFrom(r.Context()))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "")
	}
}
