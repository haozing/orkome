.PHONY: build test test-int lint tidy migrate-up migrate-down migrate-status web-dev web-build web-test web-lint

build:
	go build ./...

test:
	go test ./... -count=1

test-int:
	TEST_DATABASE_URL ?= $(DATABASE_URL)
	go test ./... -count=1 -v -run 'PG|Pgmigrate'

E2E_PG_NAME := orkome-test-pg
E2E_PG_PORT := 54329
E2E_PG_URL  := postgres://postgres:orkome_e2e@localhost:$(E2E_PG_PORT)/orkome?sslmode=disable

test-e2e:
	@docker start $(E2E_PG_NAME) >/dev/null 2>&1 || 		docker run -d --name $(E2E_PG_NAME) -e POSTGRES_PASSWORD=orkome_e2e 			-p 127.0.0.1:$(E2E_PG_PORT):5432 postgres:16-alpine >/dev/null
	@for i in $$(seq 1 30); do 		docker exec $(E2E_PG_NAME) pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
	@docker exec $(E2E_PG_NAME) psql -U postgres -c "CREATE DATABASE orkome" >/dev/null 2>&1 || true
	TEST_DATABASE_URL=$(E2E_PG_URL) go test ./e2e/ -count=1 -v

test-e2e-clean:
	@docker rm -f $(E2E_PG_NAME) >/dev/null 2>&1 && echo "removed $(E2E_PG_NAME)" || echo "not found"

lint:
	golangci-lint run

tidy:
	go mod tidy

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down 1

migrate-status:
	go run ./cmd/migrate status

web-dev:
	cd web && pnpm dev

web-build:
	cd web && pnpm build

web-test:
	cd web && pnpm test

web-lint:
	cd web && pnpm lint
