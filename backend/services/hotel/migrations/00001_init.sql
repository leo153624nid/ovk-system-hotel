-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE guests (
    id         uuid        PRIMARY KEY,
    full_name  text        NOT NULL CHECK (char_length(full_name) BETWEEN 1 AND 200),
    phone      text        NOT NULL,
    room       text        NOT NULL CHECK (char_length(room) BETWEEN 1 AND 20),
    check_in   date        NOT NULL,
    check_out  date        NOT NULL,
    note       text        NOT NULL DEFAULT '' CHECK (char_length(note) <= 2000),
    created_by uuid        NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT guests_dates_chk CHECK (check_out >= check_in)
);

-- Поиск подстроки (ILIKE '%q%') по ФИО, телефону и номеру одним запросом.
-- Выражение в запросе должно совпадать с выражением индекса символ в символ.
CREATE INDEX guests_search_trgm_idx ON guests
    USING gin ((full_name || ' ' || phone || ' ' || room) gin_trgm_ops);

-- Ключ курсорной пагинации списка.
CREATE INDEX guests_created_idx ON guests (created_at DESC, id DESC);

CREATE INDEX guests_check_in_idx ON guests (check_in);

CREATE TABLE outbox (
    id           bigserial   PRIMARY KEY,
    event_id     uuid        NOT NULL UNIQUE,
    subject      text        NOT NULL,
    payload      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX outbox_unpublished_idx ON outbox (id) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE outbox;
DROP TABLE guests;
