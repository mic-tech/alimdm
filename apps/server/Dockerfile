# ---- Stage 1: build the Go API ----
FROM golang:1.25-alpine AS go
WORKDIR /src
COPY apps/server/go.mod apps/server/go.sum* ./
RUN go mod download 2>/dev/null || true
COPY apps/server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/api ./cmd/api \
    && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/bootstrap ./cmd/bootstrap

# ---- Stage 2: build the React console (optional; skip if console/ absent) ----
FROM node:20-alpine AS console
WORKDIR /web
COPY apps/console/package.json apps/console/package-lock.json* ./
RUN npm install --no-audit --no-fund 2>/dev/null || true
COPY apps/console/ ./
RUN npm run build 2>/dev/null || true

# ---- Stage 3: runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=go /out/api /out/bootstrap ./
# console dist (if built)
COPY --from=console /web/dist/ ./console-dist/
ENV FK_ADDR=:8080 \
    FK_DB=/data/alimdm.db \
    FK_CONSOLE_DIR=/app/console-dist \
    FK_APK_ROOT=/data/apks
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/app/api"]
