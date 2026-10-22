-- +goose Up
CREATE TABLE audit_log (
    id          bigserial   PRIMARY KEY,
    event_id    uuid        NOT NULL UNIQUE, -- идемпотентность: повторная доставка события не создаёт вторую запись
    action      text        NOT NULL CHECK (action IN ('created', 'updated', 'deleted')),
    entity_id   uuid        NOT NULL,
    actor_id    uuid        NOT NULL,
    payload     jsonb       NOT NULL,
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now()
);

-- Общий журнал, новые первыми, ключ курсорной пагинации.
CREATE INDEX audit_occurred_idx ON audit_log (occurred_at DESC, id DESC);
-- История одной записи гостя.
CREATE INDEX audit_entity_idx ON audit_log (entity_id, occurred_at DESC, id DESC);

-- +goose Down
DROP TABLE audit_log;
