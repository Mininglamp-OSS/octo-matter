FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /todo-service ./cmd/main.go

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /todo-service /todo-service
COPY migrations/ /migrations/
EXPOSE 8080
CMD ["/todo-service"]
