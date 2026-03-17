# Stage 1 — Build
FROM golang:1.25-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git ca-certificates tzdata
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" -o /bin/eeapi ./cmd/eeapi

# Stage 2 — Runtime
FROM alpine:3.19

WORKDIR /app
RUN mkdir -p /app/keysStore

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /bin/eeapi /bin/eeapi

EXPOSE 8080
ENTRYPOINT ["/bin/eeapi"]