# Build stage — compile all three agent binaries as fully static executables.
# modernc.org/sqlite is pure Go so CGO_ENABLED=0 produces a genuinely static binary.
#
# Base images are pinned by sha256 digest so rebuilds are reproducible and
# supply-chain advisories can be tied to an exact image. Refresh digests with
# Renovate/Dependabot, or manually via:
#   docker pull golang:1.25-bookworm
#   docker image inspect --format='{{index .RepoDigests 0}}' golang:1.25-bookworm
FROM golang:1.25-bookworm@sha256:154bd7001b6eb339e88c964442c0ad6ed5e53f09844cc818a41ce4ecb3ce3b43 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wintermute  ./cmd/wintermute  && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/neuromancer ./cmd/neuromancer && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent       ./cmd/agent

# Runtime stage — Alpine is small (~7 MB) and ships wget so Docker health checks
# can probe the /health endpoint without adding a separate tool.
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc

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
