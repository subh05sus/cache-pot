# ---- build stage ----
FROM golang:1.25-alpine AS build
WORKDIR /src
# Cache module downloads (Cache-Pot has zero third-party deps, but this keeps the
# layer cache friendly if any are added later).
COPY go.mod ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/subh05sus/cache-pot/internal/server.Version=${VERSION}" \
    -o /out/cache-pot ./cmd/cache-pot

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/cache-pot /cache-pot
# 6379: RESP server (drop-in for REDIS_URL). 8080: web dashboard.
EXPOSE 6379 8080
# Persist snapshots to a mounted volume at /data.
VOLUME ["/data"]
ENV CACHEPOT_SNAPSHOT_PATH=/data/cache-pot.snapshot
ENTRYPOINT ["/cache-pot"]
