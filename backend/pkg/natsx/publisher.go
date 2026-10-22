// Package natsx — тонкая обёртка над JetStream для публикации событий.
package natsx

import (
	"context"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Publisher публикует сообщения в JetStream. Подключение и создание стрима ленивые,
// поэтому недоступность NATS при старте не мешает запуску сервиса: ошибка вернётся из Publish.
type Publisher struct {
	nc     *nats.Conn
	js     jetstream.JetStream
	stream jetstream.StreamConfig

	mu      sync.Mutex
	ensured bool
}

func NewPublisher(url string, stream jetstream.StreamConfig) (*Publisher, error) {
	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.Name("hotel-outbox"),
	)
	if err != nil {
		return nil, fmt.Errorf("natsx: connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("natsx: jetstream: %w", err)
	}

	return &Publisher{
		nc:     nc,
		js:     js,
		stream: stream,
	}, nil
}

// Publish отправляет data в subject. msgID используется JetStream для дедупликации повторных публикаций.
func (p *Publisher) Publish(
	ctx context.Context,
	subject string,
	msgID string,
	data []byte,
) error {
	if err := p.ensureStream(ctx); err != nil {
		return err
	}

	if _, err := p.js.Publish(ctx, subject, data, jetstream.WithMsgID(msgID)); err != nil {
		return fmt.Errorf("natsx: publish %s: %w", subject, err)
	}

	return nil
}

func (p *Publisher) ensureStream(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ensured {
		return nil
	}

	if _, err := p.js.CreateOrUpdateStream(ctx, p.stream); err != nil {
		return fmt.Errorf("natsx: ensure stream %s: %w", p.stream.Name, err)
	}

	p.ensured = true
	return nil
}

func (p *Publisher) Close() {
	_ = p.nc.Drain()
}
