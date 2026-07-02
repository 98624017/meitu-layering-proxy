# syntax=docker/dockerfile:1

ARG BUILDPLATFORM=linux/amd64
FROM --platform=${BUILDPLATFORM} golang:1.26.4-alpine3.24 AS builder

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN go test ./...

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/meitu-layering-proxy \
    ./cmd/meitu-layering-proxy

FROM alpine:3.24.1

RUN apk add --no-cache ca-certificates \
    && addgroup -S app \
    && adduser -S -D -H -u 10001 -G app app

WORKDIR /app
COPY --from=builder /out/meitu-layering-proxy /usr/local/bin/meitu-layering-proxy

ENV MEITU_LISTEN_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

USER app
ENTRYPOINT ["meitu-layering-proxy"]
