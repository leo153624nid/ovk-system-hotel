COMPOSE := docker compose --env-file .env -f deploy/docker-compose.yml
MIGRATORS := auth-migrate hotel-migrate audit-migrate
GO_MODULES := pkg services/auth services/hotel services/audit services/gateway

.DEFAULT_GOAL := help

.PHONY: help env up down logs migrate tidy build test test-unit lint fmt

help: ## Список команд
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

env: ## Создать .env из .env.example, если его нет
	@test -f .env || cp .env.example .env

up: env ## Поднять окружение
	$(COMPOSE) up -d --build --wait

down: ## Остановить окружение (данные сохраняются)
	$(COMPOSE) down

logs: ## Логи контейнеров
	$(COMPOSE) logs -f

migrate: env ## Применить миграции всех сервисов
	$(COMPOSE) up -d --wait postgres
	@for s in $(MIGRATORS); do $(COMPOSE) run --rm $$s || exit 1; done

tidy: ## go mod tidy во всех модулях
	@for m in $(GO_MODULES); do (cd backend/$$m && go mod tidy) || exit 1; done

build: ## Собрать все Go-модули
	@for m in $(GO_MODULES); do (cd backend/$$m && go build -o /dev/null ./...) || exit 1; done

test: ## Все тесты (integration требуют Docker)
	@for m in $(GO_MODULES); do (cd backend/$$m && go test -race ./...) || exit 1; done

test-unit: ## Только unit-тесты, без Docker
	@for m in $(GO_MODULES); do (cd backend/$$m && go test -race -short ./...) || exit 1; done

lint: ## golangci-lint во всех модулях
	@for m in $(GO_MODULES); do (cd backend/$$m && golangci-lint run ./...) || exit 1; done

fmt: ## gofmt во всех модулях
	@gofmt -w backend
