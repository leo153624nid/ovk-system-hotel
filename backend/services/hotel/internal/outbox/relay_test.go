package outbox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"ovk-system-hotel/services/hotel/internal/domain"
)

type fakeStore struct {
	mu      sync.Mutex
	pending []domain.OutboxEvent
	done    []uuid.UUID
}

// PublishPending повторяет контракт репозитория: останавливается на первой ошибке и помечает успешные.
func (f *fakeStore) PublishPending(ctx context.Context, limit int, publish func(context.Context, domain.OutboxEvent) error) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var sent int
	for _, ev := range f.pending {
		if sent == limit {
			break
		}
		if err := publish(ctx, ev); err != nil {
			f.pending = f.pending[sent:]
			return sent, err
		}
		f.done = append(f.done, ev.EventID)
		sent++
	}

	f.pending = f.pending[sent:]
	return sent, nil
}

func (f *fakeStore) doneCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.done)
}

type fakePublisher struct {
	mu      sync.Mutex
	failFor int // сколько первых вызовов завершить ошибкой
	calls   int
	ids     []string
}

func (p *fakePublisher) Publish(_ context.Context, _, msgID string, _ []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	if p.calls <= p.failFor {
		return errors.New("broker down")
	}

	p.ids = append(p.ids, msgID)
	return nil
}

func TestRelayDeliversAfterBrokerRecovers(t *testing.T) {
	store := &fakeStore{}
	for range 3 {
		store.pending = append(store.pending, domain.OutboxEvent{EventID: uuid.New(), Subject: "guest.created", Payload: []byte("{}")})
	}

	pub := &fakePublisher{failFor: 2}
	relay := NewRelay(store, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Millisecond, 10)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		relay.Run(ctx)
		close(done)
	}()

	deadline := time.After(3 * time.Second)
	for store.doneCount() < 3 {
		select {
		case <-deadline:
			t.Fatalf("events not delivered: %d of 3", store.doneCount())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done

	pub.mu.Lock()
	defer pub.mu.Unlock()

	if len(pub.ids) != 3 {
		t.Fatalf("published %d, want 3", len(pub.ids))
	}

	for i, id := range store.done {
		if pub.ids[i] != id.String() {
			t.Fatalf("msgID[%d] = %s, want event id %s (needed for broker dedup)", i, pub.ids[i], id)
		}
	}
}
