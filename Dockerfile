# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/meowmcp ./cmd/meowmcp \
 && mkdir -p /out/data

# static: CA certificates for the WhatsApp websocket, no shell, runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/meowmcp /usr/local/bin/meowmcp
# Owned by nonroot so a fresh named volume mounted here (docker run -v name:/data) is writable.
COPY --from=build --chown=65532:65532 /out/data /data
ENV MEOWMCP_TRANSPORT=http \
    MEOWMCP_HTTP_ADDR=0.0.0.0:8484 \
    MEOWMCP_DATA_DIR=/data
EXPOSE 8484
ENTRYPOINT ["/usr/local/bin/meowmcp"]
