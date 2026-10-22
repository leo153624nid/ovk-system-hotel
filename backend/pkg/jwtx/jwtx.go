// Package jwtx выпускает и проверяет access-токены.
// Алгоритм скрыт за интерфейсами Signer и Verifier, чтобы его можно было заменить (например, на EdDSA).
package jwtx

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const minSecretLen = 32

// ErrInvalid возвращается для любого токена, не прошедшего проверку: подпись, срок, издатель, формат.
var ErrInvalid = errors.New("jwtx: invalid token")

// Claims содержит данные проверенного токена.
type Claims struct {
	UserID    uuid.UUID
	ExpiresAt time.Time
}

// Signer выпускает токен для пользователя.
type Signer interface {
	Sign(userID uuid.UUID, ttl time.Duration) (string, error)
}

// Verifier проверяет токен и возвращает его claims.
type Verifier interface {
	Verify(token string) (Claims, error)
}

// HS256 реализует Signer и Verifier на общем секрете.
type HS256 struct {
	secret []byte
	issuer string
	now    func() time.Time
}

// NewHS256 создаёт подписывающий и проверяющий объект. Секрет короче 32 байт отвергается.
func NewHS256(secret, issuer string) (*HS256, error) {
	if len(secret) < minSecretLen {
		return nil, fmt.Errorf("jwtx: secret must be at least %d bytes", minSecretLen)
	}
	if issuer == "" {
		return nil, errors.New("jwtx: issuer is empty")
	}

	return &HS256{
		secret: []byte(secret),
		issuer: issuer,
		now:    time.Now,
	}, nil
}

// Sign выпускает токен со сроком жизни ttl.
func (h *HS256) Sign(userID uuid.UUID, ttl time.Duration) (string, error) {
	now := h.now()

	claims := jwt.RegisteredClaims{
		Issuer:    h.issuer,
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(h.secret)
}

// Verify проверяет подпись, алгоритм, издателя и срок действия.
func (h *HS256) Verify(token string) (Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(h.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(h.now),
	)

	var rc jwt.RegisteredClaims

	if _, err := parser.ParseWithClaims(token, &rc, func(*jwt.Token) (any, error) { return h.secret, nil }); err != nil {
		return Claims{}, ErrInvalid
	}

	id, err := uuid.Parse(rc.Subject)
	if err != nil {
		return Claims{}, ErrInvalid
	}

	return Claims{
		UserID:    id,
		ExpiresAt: rc.ExpiresAt.Time,
	}, nil
}
