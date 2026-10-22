package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/pkg/events"
	"ovk-system-hotel/services/audit/internal/domain"
)

type fakeStore struct {
	inserted  []domain.Entry
	insertErr error

	entries   []domain.Entry
	gotEntity *uuid.UUID
	gotLimit  int
	gotCursor *domain.Cursor
}

func (f *fakeStore) Insert(_ context.Context, e domain.Entry) error {
	if f.insertErr != nil {
		return f.insertErr
	}

	f.inserted = append(f.inserted, e)

	return nil
}

func (f *fakeStore) List(
	_ context.Context,
	entityID *uuid.UUID,
	limit int,
	after *domain.Cursor,
) ([]domain.Entry, error) {
	f.gotEntity, f.gotLimit, f.gotCursor = entityID, limit, after

	if limit > len(f.entries) {
		limit = len(f.entries)
	}

	return f.entries[:limit], nil
}

func event(typ string) events.GuestEvent {
	id := uuid.New()

	return events.GuestEvent{
		EventID:    uuid.New(),
		Type:       typ,
		OccurredAt: time.Date(2026, 10, 22, 12, 0, 0, 123456789, time.FixedZone("MSK", 3*3600)),
		ActorID:    uuid.New(),
		EntityID:   id,
		Data: events.GuestData{
			ID:       id,
			FullName: "Иван Петров",
			Room:     "204",
			CheckIn:  "2026-11-01",
			CheckOut: "2026-11-05"},
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

func TestRecordMapsEvents(t *testing.T) {
	cases := map[string]string{
		events.GuestCreated: "created",
		events.GuestUpdated: "updated",
		events.GuestDeleted: "deleted",
	}

	for typ, action := range cases {
		t.Run(typ, func(t *testing.T) {
			store := &fakeStore{}
			ev := event(typ)

			if err := New(store).Record(t.Context(), typ, marshal(t, ev)); err != nil {
				t.Fatal(err)
			}
			if len(store.inserted) != 1 {
				t.Fatalf("inserted %d entries, want 1", len(store.inserted))
			}

			got := store.inserted[0]

			if got.Action != action || got.EventID != ev.EventID || got.EntityID != ev.EntityID || got.ActorID != ev.ActorID {
				t.Fatalf("unexpected entry: %+v", got)
			}
			if got.OccurredAt.Location() != time.UTC || got.OccurredAt.Nanosecond()%1000 != 0 {
				t.Fatalf("occurred_at must be UTC and truncated to microseconds, got %v", got.OccurredAt)
			}

			var snapshot events.GuestData
			if err := json.Unmarshal(got.Payload, &snapshot); err != nil || snapshot.Room != "204" || snapshot.FullName != "Иван Петров" {
				t.Fatalf("payload must keep the guest snapshot: %s (%v)", got.Payload, err)
			}
		})
	}
}

func TestRecordRejectsInvalidEvents(t *testing.T) {
	zeroID := func(e *events.GuestEvent) { e.EventID = uuid.Nil }

	cases := []struct {
		name    string
		subject string
		data    func(t *testing.T) []byte
	}{
		{"malformed json", events.GuestCreated, func(*testing.T) []byte { return []byte("{not json") }},
		{"empty body", events.GuestCreated, func(*testing.T) []byte { return nil }},
		{"unknown type", "guest.exploded", func(t *testing.T) []byte { return marshal(t, event("guest.exploded")) }},
		{"subject differs from type", events.GuestDeleted, func(t *testing.T) []byte { return marshal(t, event(events.GuestCreated)) }},
		{"nil event_id", events.GuestCreated, func(t *testing.T) []byte { e := event(events.GuestCreated); zeroID(&e); return marshal(t, e) }},
		{"nil actor_id", events.GuestCreated, func(t *testing.T) []byte {
			e := event(events.GuestCreated)
			e.ActorID = uuid.Nil
			return marshal(t, e)
		}},
		{"missing occurred_at", events.GuestCreated, func(t *testing.T) []byte {
			e := event(events.GuestCreated)
			e.OccurredAt = time.Time{}
			return marshal(t, e)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}

			err := New(store).Record(t.Context(), tc.subject, tc.data(t))

			if !errors.Is(err, domain.ErrInvalidEvent) {
				t.Fatalf("err = %v, want ErrInvalidEvent", err)
			}
			if len(store.inserted) != 0 {
				t.Fatal("invalid event must not be stored")
			}
		})
	}
}

func TestRecordStoreErrorIsNotPermanent(t *testing.T) {
	store := &fakeStore{insertErr: errors.New("db down")}

	err := New(store).Record(t.Context(), events.GuestCreated, marshal(t, event(events.GuestCreated)))

	if err == nil || errors.Is(err, domain.ErrInvalidEvent) {
		t.Fatalf("err = %v; a storage failure must stay retryable", err)
	}
}

func TestListPagination(t *testing.T) {
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{}

	for i := 3; i >= 1; i-- {
		store.entries = append(store.entries, domain.Entry{ID: int64(i), OccurredAt: base.Add(time.Duration(i) * time.Second)})
	}
	svc := New(store)
	entity := uuid.New()

	page, err := svc.List(t.Context(), &entity, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if store.gotLimit != 3 || store.gotEntity == nil || *store.gotEntity != entity {
		t.Fatalf("store got limit=%d entity=%v; want limit+1=3 and the entity filter", store.gotLimit, store.gotEntity)
	}
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("want 2 items and a cursor, got %d / %q", len(page.Items), page.NextCursor)
	}

	c, err := decodeCursor(page.NextCursor)
	if err != nil || c.ID != page.Items[1].ID {
		t.Fatalf("cursor must point at the last returned entry: %+v, %v", c, err)
	}

	last, err := svc.List(t.Context(), nil, 3, page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if last.NextCursor != "" || store.gotCursor == nil {
		t.Fatalf("last page must have no cursor and the cursor must reach the store: %q / %+v", last.NextCursor, store.gotCursor)
	}
}

func TestCursor(t *testing.T) {
	want := domain.Cursor{OccurredAt: time.Date(2026, 10, 22, 12, 0, 0, 5000, time.UTC), ID: 42}
	got, err := decodeCursor(encodeCursor(want))

	if err != nil || !got.OccurredAt.Equal(want.OccurredAt) || got.ID != want.ID {
		t.Fatalf("round trip: got %+v, %v", got, err)
	}

	for _, s := range []string{"!!!", "e30", "bm90LWpzb24", encodeCursor(domain.Cursor{OccurredAt: want.OccurredAt, ID: 0})} {
		if _, err := decodeCursor(s); !errors.Is(err, domain.ErrInvalidCursor) {
			t.Fatalf("decodeCursor(%q) err = %v, want ErrInvalidCursor", s, err)
		}
	}

	if _, err := New(&fakeStore{}).List(t.Context(), nil, 10, "garbage!"); !errors.Is(err, domain.ErrInvalidCursor) {
		t.Fatalf("List with a bad cursor: %v", err)
	}
}
