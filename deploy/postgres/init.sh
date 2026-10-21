#!/bin/sh
# Создаёт отдельную БД и роль для каждого сервиса. Выполняется при первом старте контейнера.
set -eu

create_db() {
  db="$1"; user="$2"; pass="$3"
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<SQL
CREATE ROLE ${user} LOGIN PASSWORD '${pass}';
CREATE DATABASE ${db} OWNER ${user};
REVOKE ALL ON DATABASE ${db} FROM PUBLIC;
SQL
}

create_db auth_db  auth  "$AUTH_DB_PASSWORD"
create_db hotel_db hotel "$HOTEL_DB_PASSWORD"
create_db audit_db audit "$AUDIT_DB_PASSWORD"

# Расширение pg_trgm нужно для поиска по гостям и ставится суперпользователем.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname hotel_db -c 'CREATE EXTENSION IF NOT EXISTS pg_trgm;'
