// Package repository реализует хранилище сервиса hotel поверх PostgreSQL.
package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ovk-system-hotel/services/hotel/internal/domain"
)

const columns = `id, full_name, phone, room, check_in, check_out, note, created_by, created_at, updated_at`

// SearchExpr совпадает с выражением индекса guests_search_trgm_idx, иначе планировщик его не использует.
const SearchExpr = `(full_name || ' ' || phone || ' ' || room)`

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

func (r *Repository) Create(ctx context.Context, g domain.Guest, ev domain.EventFunc) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO guests (`+columns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			g.ID, g.FullName, g.Phone, g.Room, g.CheckIn, g.CheckOut, g.Note, g.CreatedBy, g.CreatedAt, g.UpdatedAt)

		if err != nil {
			return fmt.Errorf("insert guest: %w", err)
		}

		return insertOutbox(ctx, tx, g, ev)
	})
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (domain.Guest, error) {
	g, err := scanGuest(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM guests WHERE id = $1`, id))

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Guest{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Guest{}, fmt.Errorf("select guest: %w", err)
	}

	return g, nil
}

func (r *Repository) Update(ctx context.Context, g domain.Guest, ev domain.EventFunc) (domain.Guest, error) {
	var out domain.Guest
	err := r.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanGuest(tx.QueryRow(ctx,
			`UPDATE guests
			    SET full_name = $2, phone = $3, room = $4, check_in = $5, check_out = $6, note = $7, updated_at = $8
			  WHERE id = $1
			  RETURNING `+columns,
			g.ID, g.FullName, g.Phone, g.Room, g.CheckIn, g.CheckOut, g.Note, g.UpdatedAt))

		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("update guest: %w", err)
		}

		return insertOutbox(ctx, tx, out, ev)
	})

	if err != nil {
		return domain.Guest{}, err
	}

	return out, nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID, ev domain.EventFunc) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		g, err := scanGuest(tx.QueryRow(ctx, `DELETE FROM guests WHERE id = $1 RETURNING `+columns, id))

		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("delete guest: %w", err)
		}

		return insertOutbox(ctx, tx, g, ev)
	})
}

func (r *Repository) List(ctx context.Context, query string, limit int, after *domain.Cursor) ([]domain.Guest, error) {
	var (
		conds []string
		args  []any
	)
	if query != "" {
		args = append(args, "%"+escapeLike(query)+"%")
		conds = append(conds, fmt.Sprintf(`%s ILIKE $%d ESCAPE '\'`, SearchExpr, len(args)))
	}
	if after != nil {
		args = append(args, after.CreatedAt, after.ID)
		conds = append(conds, fmt.Sprintf(`(created_at, id) < ($%d, $%d)`, len(args)-1, len(args)))
	}
	args = append(args, limit)

	sql := `SELECT ` + columns + ` FROM guests`
	if len(conds) > 0 {
		sql += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	sql += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(args))

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list guests: %w", err)
	}
	defer rows.Close()

	var out []domain.Guest
	for rows.Next() {
		g, err := scanGuest(rows)
		if err != nil {
			return nil, fmt.Errorf("scan guest: %w", err)
		}
		out = append(out, g)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list guests: %w", err)
	}

	return out, nil
}

// PublishPending берёт до limit неопубликованных событий (FOR UPDATE SKIP LOCKED, поэтому несколько
// экземпляров не мешают друг другу), вызывает publish по порядку и помечает успешные как опубликованные.
// На первой ошибке publish останавливается: уже отправленные сохраняются, остальные будут повторены.
func (r *Repository) PublishPending(ctx context.Context, limit int, publish func(context.Context, domain.OutboxEvent) error) (int, error) {
	var (
		sent       int
		publishErr error
	)
	err := r.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, event_id, subject, payload FROM outbox
			  WHERE published_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)

		if err != nil {
			return fmt.Errorf("select outbox: %w", err)
		}

		type row struct {
			id int64
			ev domain.OutboxEvent
		}
		var pending []row

		for rows.Next() {
			var x row
			if err := rows.Scan(&x.id, &x.ev.EventID, &x.ev.Subject, &x.ev.Payload); err != nil {
				rows.Close()
				return fmt.Errorf("scan outbox: %w", err)
			}
			pending = append(pending, x)
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			return fmt.Errorf("select outbox: %w", err)
		}

		var done []int64
		for _, x := range pending {
			if publishErr = publish(ctx, x.ev); publishErr != nil {
				break
			}
			done = append(done, x.id)
		}

		sent = len(done)
		if sent == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, done); err != nil {
			return fmt.Errorf("mark published: %w", err)
		}

		return nil
	})

	if err != nil {
		return 0, err
	}

	return sent, publishErr
}

func (r *Repository) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)

	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

func insertOutbox(ctx context.Context, tx pgx.Tx, g domain.Guest, ev domain.EventFunc) error {
	e, err := ev(g)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO outbox (event_id, subject, payload) VALUES ($1, $2, $3)`, e.EventID, e.Subject, e.Payload); err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}

	return nil
}

func scanGuest(row pgx.Row) (domain.Guest, error) {
	var g domain.Guest
	err := row.Scan(
		&g.ID,
		&g.FullName,
		&g.Phone,
		&g.Room,
		&g.CheckIn,
		&g.CheckOut,
		&g.Note,
		&g.CreatedBy,
		&g.CreatedAt,
		&g.UpdatedAt,
	)
	return g, err
}

// escapeLike экранирует служебные символы шаблона LIKE, чтобы пользовательский ввод искался как текст.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
