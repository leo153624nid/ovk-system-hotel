// Package service реализует бизнес-логику регистрации, входа и ротации токенов.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"ovk-system-hotel/pkg/jwtx"
	"ovk-system-hotel/services/auth/internal/domain"
)

const (
	minPasswordLen = 8
	maxPasswordLen = 72 // ограничение bcrypt
	maxEmailLen    = 254
	refreshBytes   = 32
)

// Store описывает хранилище, нужное сервису.
type Store interface {
	CreateUser(ctx context.Context, u domain.User) error
	UserByEmail(ctx context.Context, email string) (domain.User, error)
	SaveRefresh(ctx context.Context, t domain.RefreshToken) error
	// RotateRefresh атомарно отзывает токен с хешем oldHash и сохраняет next в той же семье.
	// Повторное предъявление отозванного токена отзывает всю семью и возвращает ErrInvalidToken.
	RotateRefresh(ctx context.Context, oldHash []byte, next domain.RefreshToken, now time.Time) (uuid.UUID, error)
	RevokeFamily(ctx context.Context, hash []byte) error
}

type Config struct {
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	BcryptCost int
}

// Tokens — результат входа или обновления.
type Tokens struct {
	Access         string
	ExpiresIn      int
	Refresh        string
	RefreshExpires time.Time
}

type Service struct {
	store     Store
	signer    jwtx.Signer
	cfg       Config
	now       func() time.Time // TODO: for what ?
	dummyHash []byte           // TODO: for what ?
}

func New(store Store, signer jwtx.Signer, cfg Config) (*Service, error) {
	dummy, err := bcrypt.GenerateFromPassword([]byte("dummy-password"), cfg.BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("service: bcrypt cost: %w", err)
	}

	return &Service{
		store:     store,
		signer:    signer,
		cfg:       cfg,
		now:       time.Now,
		dummyHash: dummy,
	}, nil
}

func (s *Service) Register(ctx context.Context, email, password string) (domain.User, error) {
	email = normalizeEmail(email)

	if err := validateCredentials(email, password); err != nil {
		return domain.User{}, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}

	u := domain.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: string(hash),
		CreatedAt:    s.now(),
	}

	if err := s.store.CreateUser(ctx, u); err != nil {
		return domain.User{}, err
	}

	return u, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (Tokens, error) {
	u, err := s.store.UserByEmail(ctx, normalizeEmail(email))

	switch {
	case errors.Is(err, domain.ErrNotFound):
		// Выравниваем время ответа, чтобы не раскрывать, существует ли email.
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return Tokens{}, domain.ErrInvalidCredentials
	case err != nil:
		return Tokens{}, err
	}

	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return Tokens{}, domain.ErrInvalidCredentials
	}

	raw, tok := s.newRefresh(u.ID, uuid.New())
	if err := s.store.SaveRefresh(ctx, tok); err != nil {
		return Tokens{}, err
	}

	return s.tokens(u.ID, raw, tok.ExpiresAt)
}

func (s *Service) Refresh(ctx context.Context, rawToken string) (Tokens, error) {
	if rawToken == "" {
		return Tokens{}, domain.ErrInvalidToken
	}

	raw, next := s.newRefresh(uuid.Nil, uuid.Nil)

	userID, err := s.store.RotateRefresh(ctx, hashToken(rawToken), next, s.now())
	if err != nil {
		return Tokens{}, err
	}

	return s.tokens(userID, raw, next.ExpiresAt)
}

// Logout отзывает всю семью токенов. Неизвестный токен не считается ошибкой.
func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	return s.store.RevokeFamily(ctx, hashToken(rawToken))
}

func (s *Service) tokens(userID uuid.UUID, refresh string, refreshExp time.Time) (Tokens, error) {
	access, err := s.signer.Sign(userID, s.cfg.AccessTTL)
	if err != nil {
		return Tokens{}, fmt.Errorf("sign access token: %w", err)
	}

	return Tokens{
		Access:         access,
		ExpiresIn:      int(s.cfg.AccessTTL.Seconds()),
		Refresh:        refresh,
		RefreshExpires: refreshExp,
	}, nil
}

// newRefresh создаёт случайный refresh-токен; user и family заполняет хранилище при ротации.
func (s *Service) newRefresh(userID, familyID uuid.UUID) (string, domain.RefreshToken) {
	b := make([]byte, refreshBytes)

	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err)) // недоступность источника энтропии не восстанавливается
	}

	raw := base64.RawURLEncoding.EncodeToString(b)

	return raw, domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		Hash:      hashToken(raw),
		FamilyID:  familyID,
		ExpiresAt: s.now().Add(s.cfg.RefreshTTL),
	}
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateCredentials(email, password string) error {
	var fields []domain.FieldError

	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email || len(email) > maxEmailLen {
		fields = append(fields, domain.FieldError{Field: "email", Message: "must be a valid email address"})
	}

	if len(password) < minPasswordLen {
		fields = append(fields, domain.FieldError{Field: "password", Message: fmt.Sprintf("must be at least %d characters", minPasswordLen)})
	} else if len(password) > maxPasswordLen {
		fields = append(fields, domain.FieldError{Field: "password", Message: fmt.Sprintf("must be at most %d bytes", maxPasswordLen)})
	}

	if len(fields) > 0 {
		return &domain.ValidationError{Fields: fields}
	}

	return nil
}
