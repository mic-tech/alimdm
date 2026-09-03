# ---- Stage 1: build the Go API ----
#
# Pinned to BUILDPLATFORM and cross-compiled, rather than run under emulation
# for each target. Go cross-compiles natively, so building arm64 costs the same
# as amd64; letting BuildKit emulate this stage instead would make a QEMU'd
# compiler do the work and take many times longer for an identical binary.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS go
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY apps/server/go.mod apps/server/go.sum* ./
RUN go mod download 2>/dev/null || true
COPY apps/server/ ./
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
        go build -ldflags="-s -w" -o /out/api ./cmd/api \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
        go build -ldflags="-s -w" -o /out/bootstrap ./cmd/bootstrap

# ---- Stage 2: build the React console ----
#
# Nothing here is allowed to fail quietly. Both steps used to end in
# "2>/dev/null || true", and the source copy brings the developer's own dist/
# with it: a console that failed to build was therefore shipped as whatever
# happened to be lying in that directory, silently and with no version anyone
# could name. That is exactly what happened once the build started running the
# linter — the lint error was swallowed and a stale bundle went to the server.
#
# The build now fails the image, and dist/ is cleared first so what ships can
# only be what this stage produced.
# Also BUILDPLATFORM: this stage emits static JS and CSS, which are the same
# bytes whatever CPU produced them. Emulating npm for a second architecture
# would double the slowest part of the build to arrive at identical output.
FROM --platform=$BUILDPLATFORM node:22-alpine AS console
WORKDIR /web
COPY apps/console/package.json apps/console/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY apps/console/ ./
RUN rm -rf dist && npm run build

# ---- Stage 3: runtime ----
FROM alpine:3.24
# apk upgrade, not just add. A base image is a snapshot: alpine:3.24 was built
# in June and still shipped OpenSSL 3.5.7-r0 while 3.5.8-r0 was out, so the
# "current" tag scanned with 20 findings and the older 3.20 line — long since
# rebuilt with its backports — scanned with none. Upgrading at build time takes
# whatever is patched today instead of waiting for someone to rebuild the base.
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=go /out/api /out/bootstrap ./
# console dist (if built)
COPY --from=console /web/dist/ ./console-dist/
ENV ALIMDM_ADDR=:8080 \
    ALIMDM_DB=/data/alimdm.db \
    ALIMDM_CONSOLE_DIR=/app/console-dist \
    ALIMDM_APK_ROOT=/data/apks
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/app/api"]
