# syntax=docker/dockerfile:1

# ---- Build (fully static, pure Go: modernc sqlite + maxminddb, no cgo) ----
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# VERSION is baked in via -ldflags; without it the binary would fall back to
# `git describe`, which isn't available in the runtime image.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/gnoverse/gnockpit/web.Version=${VERSION}" \
    -o /out/gnockpit .

# ---- Runtime ----
FROM alpine:3.21
# ca-certificates: gnockpit downloads the DB-IP geo database over HTTPS.
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 gnockpit
COPY --from=build /out/gnockpit /usr/local/bin/gnockpit
USER gnockpit
EXPOSE 8080

# Point gnockpit at your node's RPC and (optionally) a mounted volume for
# persistence, e.g.:
#   docker run -p 8080:8080 -v gnockpit-data:/data ghcr.io/gnoverse/gnockpit \
#     --rpc http://your-node:26657 \
#     --db-path /data/gnockpit.db --names /data/gnockpit-names.json \
#     --geoip-db /data/gnockpit-geoip.mmdb
ENTRYPOINT ["gnockpit"]
