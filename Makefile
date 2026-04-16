.PHONY: build run test lint migrate web web-dev dev docker docker-down clean

# Go backend
build:
	go build -o golab ./cmd/golab

run: build
	./golab

test:
	go test ./...

lint:
	golangci-lint run

# Database
#
# `migrate` runs all UP migrations forward. There is no `migrate-down` target.
# GoLab is live with real user data - auto-rollbacks are forbidden.
# If a rollback is truly needed, do it manually with a reviewed plan:
#   1. Snapshot the DB first.
#   2. Write an explicit, reviewed SQL patch.
#   3. Apply it under supervision.
migrate:
	goose -dir internal/database/migrations postgres "$(GOLAB_DB_URL)" up

# Frontend
web:
	cd web && npm run build

web-dev:
	cd web && npm run dev

# Full dev (backend + frontend)
dev:
	@echo "Start backend: make run"
	@echo "Start frontend: make web-dev"
	@echo "Run both in separate terminals"

# Docker
docker:
	docker-compose up -d

docker-down:
	docker-compose down

clean:
	rm -f golab
	rm -rf web/_site
