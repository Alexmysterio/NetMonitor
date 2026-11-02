# Single NetMonitor image: API/poller, key rotation CLI, frontend and nginx.
# PostgreSQL and Redis are provided by the official images in the compose file.

FROM golang:1.23-alpine AS backend
WORKDIR /src
RUN apk add --no-cache git
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/netmonitor-core ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/netmonitor-keyrotate ./cmd/keyrotate
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/netmonitor-agent ./cmd/agent

FROM node:22-alpine AS frontend
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM nginx:1.27-alpine AS runtime
RUN apk add --no-cache ca-certificates tzdata iputils supervisor

WORKDIR /app
COPY --from=backend /out/netmonitor-core /app/netmonitor-core
COPY --from=backend /out/netmonitor-keyrotate /app/netmonitor-keyrotate
COPY --from=backend /out/netmonitor-agent /app/netmonitor-agent
COPY --from=frontend /app/dist /usr/share/nginx/html
COPY deploy/allinone/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/allinone/supervisord.conf /etc/supervisord.conf
COPY deploy/allinone/entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh \
    && mkdir -p /app/uploads/map-backgrounds /app/uploads/notification-sounds /app/reports /app/certs

EXPOSE 80 1620/udp 5514/udp 8443
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=5 \
  CMD wget -qO- http://127.0.0.1/api/v1/health || exit 1

ENTRYPOINT ["/entrypoint.sh"]
