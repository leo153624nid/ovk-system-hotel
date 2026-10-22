package natsx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ErrPermanent помечает ошибку обработчика, повторять которую бессмысленно (например, битое сообщение).
// Такое сообщение завершается без повторной доставки.
var ErrPermanent = errors.New("permanent failure")

const (
	retryDelay   = 5 * time.Second // пауза перед повторной доставкой при временной ошибке
	setupRetry   = 2 * time.Second // пауза между попытками подготовить стрим и консьюмер
	drainTimeout = 5 * time.Second
)

// Handler обрабатывает одно сообщение. nil — подтвердить; ошибка с ErrPermanent — завершить
// без повторов; любая другая ошибка — вернуть сообщение в очередь с задержкой.
type Handler func(ctx context.Context, subject string, data []byte) error

// ConsumerConfig описывает подписку.
type ConsumerConfig struct {
	URL      string
	Name     string // имя подключения для диагностики на сервере
	Stream   jetstream.StreamConfig
	Consumer jetstream.ConsumerConfig
}

// RunConsumer подключается к NATS и обрабатывает сообщения до отмены ctx. Недоступность брокера при старте
// не фатальна: подготовка стрима и консьюмера повторяется, а потеря соединения восстанавливается клиентом.
func RunConsumer(ctx context.Context, log *slog.Logger, cfg ConsumerConfig, h Handler) error {
	nc, err := nats.Connect(cfg.URL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.Name(cfg.Name),
	)
	if err != nil {
		return fmt.Errorf("natsx: connect: %w", err)
	}
	defer func() { _ = nc.Drain() }()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("natsx: jetstream: %w", err)
	}

	var cc jetstream.ConsumeContext
	for {
		cc, err = subscribe(ctx, log, js, cfg, h)
		if err == nil {
			break
		}

		log.Warn("nats consumer setup failed, will retry", "error", err)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(setupRetry):
		}
	}
	log.Info("nats consumer started", "stream", cfg.Stream.Name, "consumer", cfg.Consumer.Durable)

	<-ctx.Done()
	cc.Drain()

	select {
	case <-cc.Closed():
	case <-time.After(drainTimeout):
	}

	return nil
}

func subscribe(
	ctx context.Context,
	log *slog.Logger,
	js jetstream.JetStream,
	cfg ConsumerConfig,
	h Handler,
) (jetstream.ConsumeContext, error) {
	setupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if _, err := js.CreateOrUpdateStream(setupCtx, cfg.Stream); err != nil {
		return nil, fmt.Errorf("ensure stream: %w", err)
	}

	cons, err := js.CreateOrUpdateConsumer(setupCtx, cfg.Stream.Name, cfg.Consumer)
	if err != nil {
		return nil, fmt.Errorf("ensure consumer: %w", err)
	}

	return cons.Consume(func(msg jetstream.Msg) {
		settle(ctx, log, msg, h(ctx, msg.Subject(), msg.Data()))
	}, jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
		log.Warn("nats consume error", "error", err)
	}))
}

func settle(ctx context.Context, log *slog.Logger, msg jetstream.Msg, err error) {
	var ackErr error
	switch {
	case err == nil:
		ackErr = msg.Ack()

	case errors.Is(err, ErrPermanent):
		log.ErrorContext(ctx, "message dropped as unprocessable", "subject", msg.Subject(), "error", err)
		ackErr = msg.Term()

	default:
		log.WarnContext(ctx, "message handling failed, will be redelivered", "subject", msg.Subject(), "error", err)
		ackErr = msg.NakWithDelay(retryDelay)
	}

	if ackErr != nil {
		log.WarnContext(ctx, "message settle failed", "error", ackErr)
	}
}
