// Package domain содержит сущности и ошибки сервиса hotel.
package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("guest not found")
	ErrInvalidCursor = errors.New("invalid cursor")
)

// Guest — запись в книге. Даты хранятся как полночь UTC.
type Guest struct {
	ID        uuid.UUID
	FullName  string
	Phone     string // E.164
	Room      string
	CheckIn   time.Time
	CheckOut  time.Time
	Note      string
	CreatedBy uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OutboxEvent — событие, которое записывается в одной транзакции с изменением гостя.
type OutboxEvent struct {
	EventID uuid.UUID
	Subject string
	Payload []byte
}

// EventFunc строит событие по состоянию гостя после изменения (для удаления — перед ним).
type EventFunc func(Guest) (OutboxEvent, error)

// Cursor указывает позицию в списке, отсортированном по (created_at, id) по убыванию.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type FieldError struct {
	Field   string
	Message string
}

// ValidationError описывает некорректные входные данные.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))

	for i, f := range e.Fields {
		parts[i] = f.Field + ": " + f.Message
	}

	return "validation failed: " + strings.Join(parts, "; ")
}
