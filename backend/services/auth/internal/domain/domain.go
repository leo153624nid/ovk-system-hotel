// Package domain содержит сущности и ошибки сервиса auth.
package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidToken       = errors.New("invalid refresh token")
)

type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// RefreshToken хранит только хеш токена; сам токен знает только клиент.
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Hash      []byte
	FamilyID  uuid.UUID
	ExpiresAt time.Time
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
