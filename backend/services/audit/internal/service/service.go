// Package service реализует запись событий в журнал и чтение журнала.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/pkg/events"
	"ovk-system-hotel/services/audit/internal/domain"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

var actions = map[string]string{
	events.GuestCreated: "created",
	events.GuestUpdated: "updated",
	events.GuestDeleted: "deleted",
}

// Store описывает хранилище журнала.
type Store interface {
	// Insert сохраняет запись; повторная вставка с тем же EventID молча игнорируется.
	Insert(ctx context.Context, e domain.Entry) error
	// List возвращает не более limit записей, новые первыми. entityID и after необязательны.
	List(ctx context.Context, entityID *uuid.UUID, limit int, after *domain.Cursor) ([]domain.Entry, error)
}

type Service struct {
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
}

// Record разбирает сообщение с событием и записывает его в журнал. Для сообщений, которые никогда не
// удастся разобрать, возвращает ошибку, оборачивающую domain.ErrInvalidEvent.
func (s *Service) Record(ctx context.Context, subject string, data []byte) error {
	e, err := parse(subject, data)

	if err != nil {
		return err
	}

	return s.store.Insert(ctx, e)
}

func parse(subject string, data []byte) (domain.Entry, error) {
	var ev events.GuestEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return domain.Entry{}, fmt.Errorf("%w: malformed json: %w", domain.ErrInvalidEvent, err)
	}

	action, ok := actions[ev.Type]

	switch {
	case !ok:
		return domain.Entry{}, fmt.Errorf("%w: unknown type %q", domain.ErrInvalidEvent, ev.Type)
	case subject != ev.Type:
		return domain.Entry{}, fmt.Errorf("%w: subject %q does not match type %q", domain.ErrInvalidEvent, subject, ev.Type)
	case ev.EventID == uuid.Nil || ev.EntityID == uuid.Nil || ev.ActorID == uuid.Nil:
		return domain.Entry{}, fmt.Errorf("%w: event_id, entity_id and actor_id are required", domain.ErrInvalidEvent)
	case ev.OccurredAt.IsZero():
		return domain.Entry{}, fmt.Errorf("%w: occurred_at is required", domain.ErrInvalidEvent)
	}

	payload, err := json.Marshal(ev.Data)
	if err != nil {
		return domain.Entry{}, fmt.Errorf("%w: data: %w", domain.ErrInvalidEvent, err)
	}

	return domain.Entry{
		EventID:    ev.EventID,
		Action:     action,
		EntityID:   ev.EntityID,
		ActorID:    ev.ActorID,
		Payload:    payload,
		OccurredAt: ev.OccurredAt.UTC().Truncate(time.Microsecond),
	}, nil
}

// Page — страница журнала. NextCursor пуст на последней странице.
type Page struct {
	Items      []domain.Entry
	NextCursor string
}

// List возвращает журнал, новые записи первыми. limit должен быть в диапазоне 1..MaxLimit.
func (s *Service) List(
	ctx context.Context,
	entityID *uuid.UUID,
	limit int,
	cursor string,
) (Page, error) {
	var after *domain.Cursor
	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		after = &c
	}

	items, err := s.store.List(ctx, entityID, limit+1, after)
	if err != nil {
		return Page{}, err
	}

	page := Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]

		page.NextCursor = encodeCursor(domain.Cursor{OccurredAt: last.OccurredAt, ID: last.ID})
	}

	return page, nil
}

type cursorJSON struct {
	T  time.Time `json:"t"`
	ID int64     `json:"id"`
}

func encodeCursor(c domain.Cursor) string {
	raw, _ := json.Marshal(cursorJSON{T: c.OccurredAt, ID: c.ID})

	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (domain.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return domain.Cursor{}, domain.ErrInvalidCursor
	}

	var c cursorJSON
	if err := json.Unmarshal(raw, &c); err != nil || c.ID <= 0 || c.T.IsZero() {
		return domain.Cursor{}, domain.ErrInvalidCursor
	}

	return domain.Cursor{OccurredAt: c.T, ID: c.ID}, nil
}
