# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/loki-mcp .

FROM alpine:latest

WORKDIR /app

COPY --from=builder /out/loki-mcp .

EXPOSE 8080

ENTRYPOINT ["./loki-mcp"]

# Serve HTTP on $PORT (default 8080) by default; `docker run -i <image> stdio`
# overrides this for the stdio transport.
CMD ["http"]
