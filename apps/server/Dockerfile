# ---- Stage 1: build the Go API ----
FROM golang:1.25-alpine AS go
WORKDIR /src
COPY apps/server/go.mod apps/server/go.sum* ./
RUN go mod download 2>/dev/null || true
COPY apps/server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/api ./cmd/api \
    && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/bootstrap ./cmd/bootstrap

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
FROM node:20-alpine AS console
WORKDIR /web
COPY apps/console/package.json apps/console/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY apps/console/ ./
RUN rm -rf dist && npm run build

# ---- Stage 3: runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=go /out/api /out/bootstrap ./
# console dist (if built)
COPY --from=console /web/dist/ ./console-dist/
# DELIBERATELY still FK_, not ALIMDM_. These are image defaults, and env() reads
# ALIMDM_<name> FIRST, falling back to FK_<name>. The running production server
# is started with an env file that still says FK_DB=/data/freekiosk.db — a
# database named before the rename. Renaming these to ALIMDM_DB would make the
# image's own default WIN over that override, and the server would quietly open
# an empty /data/alimdm.db: console with no devices, every tablet unable to
# authenticate, and the real database sitting untouched on disk next to it.
# Change these only together with the deployment's env file, never on their own.
ENV FK_ADDR=:8080 \
    FK_DB=/data/alimdm.db \
    FK_CONSOLE_DIR=/app/console-dist \
    FK_APK_ROOT=/data/apks
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/app/api"]
