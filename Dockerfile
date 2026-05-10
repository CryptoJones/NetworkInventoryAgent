# Build stage — compile all three agent binaries as fully static executables.
# modernc.org/sqlite is pure Go so CGO_ENABLED=0 produces a genuinely static binary.
FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wintermute  ./cmd/wintermute  && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/neuromancer ./cmd/neuromancer && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent       ./cmd/agent

# Runtime stage — Alpine is small (~7 MB) and ships wget so Docker health checks
# can probe the /health endpoint without adding a separate tool.
FROM alpine:3.20

RUN addgroup -S inventory && adduser -S -G inventory inventory

COPY --from=build /out/wintermute  /usr/local/bin/wintermute
COPY --from=build /out/neuromancer /usr/local/bin/neuromancer
COPY --from=build /out/agent       /usr/local/bin/agent

# Default data directory; override with a volume mount.
RUN mkdir -p /data && chown inventory:inventory /data
VOLUME ["/data"]

USER inventory

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/agent"]
