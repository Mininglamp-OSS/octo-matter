# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder
WORKDIR /app

# Cache module downloads as a separate layer so code edits don't bust the dep cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /todo-service ./cmd

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata wget \
  && adduser -D -u 10001 appuser

COPY --from=builder /todo-service /usr/local/bin/todo-service

USER appuser
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/usr/local/bin/todo-service"]
