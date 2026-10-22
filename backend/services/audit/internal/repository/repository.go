// Package repository реализует хранилище журнала поверх PostgreSQL.
package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ovk-system-hotel/services/audit/internal/domain"
)

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

func (r *Repository) Insert(ctx context.Context, e domain.Entry) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO audit_log (event_id, action, entity_id, actor_id, payload, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (event_id) DO NOTHING`,
		e.EventID, e.Action, e.EntityID, e.ActorID, e.Payload, e.OccurredAt)

	if err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}

	return nil
}

func (r *Repository) List(
	ctx context.Context,
	entityID *uuid.UUID,
	limit int,
	after *domain.Cursor,
) ([]domain.Entry, error) {
	var (
		conds []string
		args  []any
	)
	if entityID != nil {
		args = append(args, *entityID)
		conds = append(conds, fmt.Sprintf("entity_id = $%d", len(args)))
	}
	if after != nil {
		args = append(args, after.OccurredAt, after.ID)
		conds = append(conds, fmt.Sprintf("(occurred_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, limit)

	sql := `SELECT id, event_id, action, entity_id, actor_id, payload, occurred_at FROM audit_log`
	if len(conds) > 0 {
		sql += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	sql += fmt.Sprintf(` ORDER BY occurred_at DESC, id DESC LIMIT $%d`, len(args))

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()

	var out []domain.Entry
	for rows.Next() {
		var e domain.Entry
		if err := rows.Scan(&e.ID, &e.EventID, &e.Action, &e.EntityID, &e.ActorID, &e.Payload, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		out = append(out, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}

	return out, nil
}
