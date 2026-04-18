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
# postgresql16-client provides pg_dump and psql, needed by the
# Sprint 13 admin "Database" UI (manual backup, export, import).
#
# Version pin matters: alpine:3.21 ships postgresql17-client as the
# default when you ask for the generic "postgresql-client" alias, but
# our db service runs postgres:16-alpine. A v17 pg_dump writes dumps
# with v17-only options that v16 psql cannot replay, so the client
# must match the server's major version. Alpine exposes each major
# as its own package (postgresql15-client / 16-client / 17-client),
# so we name the v16 one explicitly and will bump this line in
# lockstep with any future Postgres upgrade.
RUN apk add --no-cache ca-certificates postgresql16-client
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
