// Package repository реализует хранилище сервиса auth поверх PostgreSQL.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"ovk-system-hotel/services/auth/internal/domain"
)

const uniqueViolation = "23505" // TODO: for what ?

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

func (r *Repository) CreateUser(ctx context.Context, u domain.User) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, $3, $4)`,
		u.ID, u.Email, u.PasswordHash, u.CreatedAt)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return domain.ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}

	return nil
}

func (r *Repository) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	var u domain.User
	err := r.pool.QueryRow(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE lower(email) = lower($1)`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("select user: %w", err)
	}

	return u, nil
}

func (r *Repository) SaveRefresh(ctx context.Context, t domain.RefreshToken) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, family_id, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		t.ID, t.UserID, t.Hash, t.FamilyID, t.ExpiresAt)

	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}

	return nil
}

func (r *Repository) RotateRefresh(
	ctx context.Context,
	oldHash []byte,
	next domain.RefreshToken,
	now time.Time,
) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id, userID, familyID uuid.UUID
		expiresAt            time.Time
		revokedAt            *time.Time
	)
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, family_id, expires_at, revoked_at FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, oldHash).
		Scan(&id, &userID, &familyID, &expiresAt, &revokedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, domain.ErrInvalidToken
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("select refresh token: %w", err)
	}

	if revokedAt != nil {
		// Повторное использование: считаем токен скомпрометированным и закрываем всю семью.
		if _, err := tx.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`, familyID, now); err != nil {
			return uuid.Nil, fmt.Errorf("revoke family: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, fmt.Errorf("commit: %w", err)
		}

		return uuid.Nil, domain.ErrInvalidToken
	}
	if !expiresAt.After(now) {
		return uuid.Nil, domain.ErrInvalidToken
	}

	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE id = $1`, id, now); err != nil {
		return uuid.Nil, fmt.Errorf("revoke token: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, family_id, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		next.ID, userID, next.Hash, familyID, next.ExpiresAt); err != nil {
		return uuid.Nil, fmt.Errorf("insert rotated token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit: %w", err)
	}

	return userID, nil
}

func (r *Repository) RevokeFamily(ctx context.Context, hash []byte) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE revoked_at IS NULL
		   AND family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1)`, hash)

	if err != nil {
		return fmt.Errorf("revoke family: %w", err)
	}

	return nil
}
