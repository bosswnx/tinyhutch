# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.25-alpine AS build

WORKDIR /app

# No third-party dependencies yet, but this keeps the layer cache useful if
# go.mod ever grows.
COPY go.mod ./
RUN go mod download

COPY src ./src

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/tinyhutch ./src

# ---- runtime stage ----
FROM alpine:3.22

RUN apk add --no-cache ca-certificates jq su-exec tzdata \
 && addgroup -S -g 1000 tinyhutch \
 && adduser -S -D -H -u 1000 -G tinyhutch tinyhutch \
 && mkdir -p /data \
 && chown 1000:1000 /data

COPY --from=build /out/tinyhutch /usr/local/bin/tinyhutch
COPY docker/entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 0755 /usr/local/bin/tinyhutch /usr/local/bin/docker-entrypoint.sh

WORKDIR /data
VOLUME ["/data"]
EXPOSE 7500

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD sh -c 'port=$(jq -r ".listen_port // 7500" /data/config.json 2>/dev/null || echo 7500); wget -q -O /dev/null "http://127.0.0.1:${port}/"'

# The entrypoint runs as root only long enough to fix /data ownership, then
# drops to the unprivileged tinyhutch user (uid/gid 1000).
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
