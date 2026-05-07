FROM golang:1.26-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
	-ldflags="-s -w" \
	-o /out/openmeteo-cache \
	./cmd/openmeteo-cache

FROM alpine:3

RUN apk add --no-cache ca-certificates wget tzdata \
	&& addgroup -S -g 65532 omproxy \
	&& adduser -S -D -H -u 65532 -G omproxy omproxy

WORKDIR /app

COPY --from=builder /out/openmeteo-cache /usr/local/bin/openmeteo-cache

USER 65532:65532

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=6 \
	CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/openmeteo-cache", "-config", "/app/config.toml"]
