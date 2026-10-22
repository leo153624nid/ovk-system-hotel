// Package domain содержит сущности и ошибки сервиса audit.
package domain

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidEvent означает, что событие некорректно и повторная доставка не поможет.
	ErrInvalidEvent  = errors.New("invalid event")
	ErrInvalidCursor = errors.New("invalid cursor")
)

// Entry — запись журнала об одном изменении.
type Entry struct {
	ID         int64
	EventID    uuid.UUID
	Action     string // created | updated | deleted
	EntityID   uuid.UUID
	ActorID    uuid.UUID
	Payload    json.RawMessage
	OccurredAt time.Time
}

// Cursor указывает позицию в списке, отсортированном по (occurred_at, id) по убыванию.
type Cursor struct {
	OccurredAt time.Time
	ID         int64
}
