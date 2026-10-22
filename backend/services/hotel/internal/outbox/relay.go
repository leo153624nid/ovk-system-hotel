// Package outbox переносит события из таблицы outbox в брокер.
package outbox

import (
	"context"
	"log/slog"
	"time"

	"ovk-system-hotel/services/hotel/internal/domain"
)

const publishTimeout = 5 * time.Second

// Store выбирает и помечает опубликованные события.
type Store interface {
	PublishPending(ctx context.Context, limit int, publish func(context.Context, domain.OutboxEvent) error) (int, error)
}

// Publisher отправляет событие в брокер. msgID нужен для дедупликации на стороне брокера.
type Publisher interface {
	Publish(ctx context.Context, subject, msgID string, data []byte) error
}

type Relay struct {
	store     Store
	publisher Publisher
	log       *slog.Logger
	interval  time.Duration
	batch     int
}

func NewRelay(
	store Store,
	publisher Publisher,
	log *slog.Logger,
	interval time.Duration,
	batch int,
) *Relay {
	return &Relay{
		store:     store,
		publisher: publisher,
		log:       log,
		interval:  interval,
		batch:     batch,
	}
}

// Run крутит цикл доставки до отмены ctx. Недоступность брокера не останавливает цикл:
// события остаются в таблице и уходят после восстановления связи.
func (r *Relay) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		n, err := r.store.PublishPending(ctx, r.batch, func(ctx context.Context, ev domain.OutboxEvent) error {
			ctx, cancel := context.WithTimeout(ctx, publishTimeout)
			defer cancel()

			return r.publisher.Publish(ctx, ev.Subject, ev.EventID.String(), ev.Payload)
		})

		if err != nil && ctx.Err() == nil {
			r.log.Warn("outbox publish failed", "error", err, "published", n)
		}
		if n > 0 {
			r.log.Debug("outbox published", "count", n)
		}

		wait := r.interval
		if err == nil && n == r.batch {
			wait = 0 // очередь не пуста, продолжаем без паузы
		}
		timer.Reset(wait)
	}
}
