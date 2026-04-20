FROM golang:1.25-alpine AS builder
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 1001 synx

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-w -s -X main.Version=${VERSION}" \
    -o /bin/eeapi ./cmd/eeapi


FROM alpine:3.21
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1001 synx && \
    mkdir -p /app/keysStore && \
    chown -R synx:synx /app

COPY --from=builder /bin/eeapi /bin/eeapi

USER synx

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["/bin/eeapi"]