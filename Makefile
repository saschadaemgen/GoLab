.PHONY: build run test lint migrate migrate-down web web-dev dev docker docker-down clean

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
migrate:
	goose -dir internal/database/migrations postgres "$(GOLAB_DB_URL)" up

migrate-down:
	goose -dir internal/database/migrations postgres "$(GOLAB_DB_URL)" down

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
