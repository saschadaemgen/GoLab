FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o golab ./cmd/golab

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/golab .
COPY --from=builder /app/internal/database/migrations ./internal/database/migrations

EXPOSE 3000
CMD ["./golab"]
