package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/pkg/events"
	"ovk-system-hotel/services/hotel/internal/domain"
)

func mustTime(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 10, 22, 12, 0, 0, 123000, time.UTC)
}

func mustUUID(t *testing.T) uuid.UUID {
	t.Helper()
	return uuid.New()
}

type fakeStore struct {
	guests []domain.Guest
	events []domain.OutboxEvent

	gotLimit  int
	gotQuery  string
	gotCursor *domain.Cursor
}

func (f *fakeStore) Create(_ context.Context, g domain.Guest, ev domain.EventFunc) error {
	e, err := ev(g)
	if err != nil {
		return err
	}

	f.guests = append(f.guests, g)
	f.events = append(f.events, e)

	return nil
}

func (f *fakeStore) Get(context.Context, uuid.UUID) (domain.Guest, error) {
	return domain.Guest{}, domain.ErrNotFound
}

func (f *fakeStore) Update(context.Context, domain.Guest, domain.EventFunc) (domain.Guest, error) {
	return domain.Guest{}, domain.ErrNotFound
}

func (f *fakeStore) Delete(context.Context, uuid.UUID, domain.EventFunc) error {
	return domain.ErrNotFound
}

func (f *fakeStore) List(_ context.Context, q string, limit int, after *domain.Cursor) ([]domain.Guest, error) {
	f.gotQuery, f.gotLimit, f.gotCursor = q, limit, after

	if limit > len(f.guests) {
		limit = len(f.guests)
	}

	return f.guests[:limit], nil
}

func TestCreateWritesEventWithActor(t *testing.T) {
	store := &fakeStore{}
	svc := New(store)
	actor := uuid.New()

	g, err := svc.Create(t.Context(), actor, validInput())
	if err != nil {
		t.Fatal(err)
	}
	if g.CreatedBy != actor || g.Phone != "+79991234567" {
		t.Fatalf("unexpected guest: %+v", g)
	}
	if len(store.events) != 1 {
		t.Fatalf("events = %d, want 1", len(store.events))
	}

	ev := store.events[0]
	if ev.Subject != events.GuestCreated {
		t.Fatalf("subject = %q", ev.Subject)
	}

	var got events.GuestEvent
	if err := json.Unmarshal(ev.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.ActorID != actor || got.EntityID != g.ID || got.EventID != ev.EventID ||
		got.Type != events.GuestCreated || got.Data.CheckIn != "2026-11-01" || got.Data.Room != "204" {
		t.Fatalf("bad event payload: %+v", got)
	}
}

func TestCreateInvalidInputWritesNothing(t *testing.T) {
	store := &fakeStore{}

	if _, err := New(store).Create(t.Context(), uuid.New(), GuestInput{}); err == nil {
		t.Fatal("expected validation error")
	}
	if len(store.guests)+len(store.events) != 0 {
		t.Fatal("nothing must be stored for invalid input")
	}
}

func TestListPagination(t *testing.T) {
	base := mustTime(t)
	store := &fakeStore{}

	for i := range 3 {
		store.guests = append(store.guests, domain.Guest{ID: uuid.New(), CreatedAt: base.Add(-time.Duration(i) * time.Second)})
	}
	svc := New(store)

	page, err := svc.List(t.Context(), "  иван ", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if store.gotLimit != 3 || store.gotQuery != "иван" {
		t.Fatalf("store got limit=%d query=%q; want limit+1=3 and trimmed query", store.gotLimit, store.gotQuery)
	}
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("want 2 items and a next cursor, got %d / %q", len(page.Items), page.NextCursor)
	}

	c, err := decodeCursor(page.NextCursor)
	if err != nil || c.ID != page.Items[1].ID {
		t.Fatalf("cursor must point at the last returned item: %+v, %v", c, err)
	}

	last, err := svc.List(t.Context(), "", 3, page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if last.NextCursor != "" || store.gotCursor == nil {
		t.Fatalf("last page must have no cursor, and the cursor must reach the store: %q, %+v", last.NextCursor, store.gotCursor)
	}
}

func TestListRejectsBadCursor(t *testing.T) {
	if _, err := New(&fakeStore{}).List(t.Context(), "", 10, "garbage!"); err == nil {
		t.Fatal("expected error for invalid cursor")
	}
}
