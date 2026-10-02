FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bot ./cmd/bot

FROM alpine:3.21
WORKDIR /app
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 botuser && mkdir -p /app/data && chown botuser:botuser /app/data
COPY --from=builder /bot /app/bot
USER botuser
EXPOSE 8080
VOLUME ["/app/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD wget -q --spider "http://localhost:${PORT:-8080}/health" || exit 1
ENTRYPOINT ["/app/bot"]
