// Package consumer связывает сообщения NATS с бизнес-логикой сервиса.
package consumer

import (
	"context"
	"errors"
	"fmt"

	"ovk-system-hotel/pkg/natsx"
	"ovk-system-hotel/services/audit/internal/domain"
)

// Recorder записывает событие в журнал.
type Recorder interface {
	Record(ctx context.Context, subject string, data []byte) error
}

// Handler возвращает обработчик сообщений: некорректное событие помечается как непоправимое
// (не будет доставлено повторно), остальные ошибки считаются временными.
func Handler(r Recorder) natsx.Handler {
	return func(ctx context.Context, subject string, data []byte) error {
		err := r.Record(ctx, subject, data)

		if errors.Is(err, domain.ErrInvalidEvent) {
			return fmt.Errorf("%w: %w", natsx.ErrPermanent, err)
		}

		return err
	}
}
