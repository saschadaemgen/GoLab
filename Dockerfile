FROM golang:1.24-alpine AS builder

WORKDIR /app

# Pre-fetch deps in a cached layer when only source changes.
# Tolerate transient "can't reach proxy" errors; tidy below will reconcile.
COPY go.mod go.sum ./
RUN go mod download || true

# Now bring in the full source.
COPY . .

# Keep go.sum / module graph in sync with the source tree before building.
RUN go mod tidy

RUN CGO_ENABLED=0 go build -o golab ./cmd/golab

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app

# Binary.
COPY --from=builder /app/golab .

# Migrations are run at startup - the SQL files must be present next to the binary.
COPY --from=builder /app/internal/database/migrations ./internal/database/migrations

# Templates + static assets (CSS, JS, uploads dir). Without this the server
# fails to parse templates and serves no CSS/JS.
COPY --from=builder /app/web ./web

EXPOSE 3000
CMD ["./golab"]
