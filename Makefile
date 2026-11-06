# Use "docker compose" when the plugin is available, otherwise the standalone "docker-compose".
COMPOSE ?= $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo docker-compose)

DATABASE_URL      ?= postgres://cdp:cdp@localhost:5544/cdp?sslmode=disable
TEST_DATABASE_URL ?= postgres://cdp:cdp@localhost:5544/cdp_test?sslmode=disable
BENCH_DATABASE_URL ?= postgres://cdp:cdp@localhost:5544/cdp_bench?sslmode=disable
export DATABASE_URL TEST_DATABASE_URL

.PHONY: help db-up db-down db-reset run test test-unit vet fmt psql bench-db seed bench

help: ## list the commands
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

db-up: ## start Postgres and wait until it is healthy
	$(COMPOSE) up -d --wait db

db-down: ## stop Postgres (data is kept)
	$(COMPOSE) down

db-reset: ## delete all local data and start Postgres again
	$(COMPOSE) down -v
	$(COMPOSE) up -d --wait db

run: ## start the API on :8080
	go run ./cmd/api

# -p 1 runs one package at a time: the database tests of different packages share cdp_test.
test: ## run all tests, including the ones that need Postgres
	go test -count=1 -p 1 ./...

test-unit: ## run only the tests that need no database
	TEST_DATABASE_URL= go test ./...

vet: ## static checks
	go vet ./...

fmt: ## format the code
	gofmt -l -w .

psql: ## open a SQL shell on the local database
	psql "$(DATABASE_URL)"

bench-db: ## create the cdp_bench database if it does not exist
	@$(COMPOSE) exec -T db psql -U cdp -d cdp -tAc "SELECT 1 FROM pg_database WHERE datname = 'cdp_bench'" | grep -q 1 \
		|| $(COMPOSE) exec -T db psql -U cdp -d cdp -c "CREATE DATABASE cdp_bench"

seed: bench-db ## load 50k users and 1M events into cdp_bench (replaces its data, ~30 s)
	DATABASE_URL="$(BENCH_DATABASE_URL)" go run ./cmd/seed -reset

# "before" takes about 2 minutes: without an index on events.user_id, deleting users is slow (that is one of the results).
bench: ## measure the queries without and with the indexes (run make seed first)
	DATABASE_URL="$(BENCH_DATABASE_URL)" go run ./cmd/bench -schema before -out docs/perf/before
	DATABASE_URL="$(BENCH_DATABASE_URL)" go run ./cmd/bench -schema after -out docs/perf/after
