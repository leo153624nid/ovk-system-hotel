// Package service реализует бизнес-логику записной книжки гостей.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/pkg/events"
	"ovk-system-hotel/services/hotel/internal/domain"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Store описывает хранилище. Create, Update и Delete записывают событие в outbox в той же транзакции.
type Store interface {
	Create(ctx context.Context, g domain.Guest, ev domain.EventFunc) error
	Get(ctx context.Context, id uuid.UUID) (domain.Guest, error)
	Update(ctx context.Context, g domain.Guest, ev domain.EventFunc) (domain.Guest, error)
	Delete(ctx context.Context, id uuid.UUID, ev domain.EventFunc) error
	// List возвращает не более limit записей, отсортированных по (created_at, id) по убыванию.
	List(ctx context.Context, query string, limit int, after *domain.Cursor) ([]domain.Guest, error)
}

type Service struct {
	store Store
	now   func() time.Time
}

func New(store Store) *Service {
	return &Service{
		store: store,
		now:   time.Now,
	}
}

// Page — страница списка. NextCursor пуст на последней странице.
type Page struct {
	Items      []domain.Guest
	NextCursor string
}

func (s *Service) Create(ctx context.Context, actor uuid.UUID, in GuestInput) (domain.Guest, error) {
	p, err := validate(in)
	if err != nil {
		return domain.Guest{}, err
	}

	now := s.timestamp()
	g := domain.Guest{
		ID:        uuid.New(),
		FullName:  p.fullName,
		Phone:     p.phone,
		Room:      p.room,
		CheckIn:   p.checkIn,
		CheckOut:  p.checkOut,
		Note:      p.note,
		CreatedBy: actor,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.store.Create(ctx, g, s.event(events.GuestCreated, actor, now)); err != nil {
		return domain.Guest{}, err
	}

	return g, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (domain.Guest, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, actor, id uuid.UUID, in GuestInput) (domain.Guest, error) {
	p, err := validate(in)
	if err != nil {
		return domain.Guest{}, err
	}

	now := s.timestamp()
	g := domain.Guest{
		ID:        id,
		FullName:  p.fullName,
		Phone:     p.phone,
		Room:      p.room,
		CheckIn:   p.checkIn,
		CheckOut:  p.checkOut,
		Note:      p.note,
		UpdatedAt: now,
	}

	return s.store.Update(ctx, g, s.event(events.GuestUpdated, actor, now))
}

func (s *Service) Delete(ctx context.Context, actor, id uuid.UUID) error {
	return s.store.Delete(ctx, id, s.event(events.GuestDeleted, actor, s.timestamp()))
}

// List ищет по подстроке query в ФИО, телефоне и номере. limit должен быть в диапазоне 1..MaxLimit.
func (s *Service) List(ctx context.Context, query string, limit int, cursor string) (Page, error) {
	var after *domain.Cursor
	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		after = &c
	}

	items, err := s.store.List(ctx, strings.TrimSpace(query), limit+1, after)
	if err != nil {
		return Page{}, err
	}

	page := Page{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(domain.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}

	return page, nil
}

// timestamp округляет время до микросекунд, как хранит PostgreSQL, чтобы курсор совпадал со значением в БД.
func (s *Service) timestamp() time.Time {
	return s.now().UTC().Truncate(time.Microsecond)
}

func (s *Service) event(typ string, actor uuid.UUID, at time.Time) domain.EventFunc {
	return func(g domain.Guest) (domain.OutboxEvent, error) {
		ev := events.GuestEvent{
			EventID:    uuid.New(),
			Type:       typ,
			OccurredAt: at,
			ActorID:    actor,
			EntityID:   g.ID,
			Data: events.GuestData{
				ID: g.ID, FullName: g.FullName, Phone: g.Phone, Room: g.Room,
				CheckIn: g.CheckIn.Format(dateLayout), CheckOut: g.CheckOut.Format(dateLayout),
				Note: g.Note, CreatedBy: g.CreatedBy, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
			},
		}

		payload, err := json.Marshal(ev)
		if err != nil {
			return domain.OutboxEvent{}, fmt.Errorf("marshal event: %w", err)
		}

		return domain.OutboxEvent{EventID: ev.EventID, Subject: typ, Payload: payload}, nil
	}
}
