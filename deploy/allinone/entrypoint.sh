#!/bin/sh
set -eu

log() {
  printf '[entrypoint] %s\n' "$*"
}

# Route RFC1918 destinations through the LAN/VPN gateway attached to the
# container instead of Docker's default gateway. Public traffic keeps using
# the default route. The routes are idempotent and can be disabled by leaving
# PRIVATE_ROUTE_GATEWAY empty.
apply_private_routes() {
  gateway="${PRIVATE_ROUTE_GATEWAY:-}"
  if [ -z "$gateway" ]; then
    log "rutas privadas desactivadas (PRIVATE_ROUTE_GATEWAY vacío)"
    return 0
  fi

  dev="${PRIVATE_ROUTE_DEV:-}"
  if [ -z "$dev" ]; then
    route=$(ip route get "$gateway" 2>/dev/null | head -n 1 || true)
    case "$route" in
      ""|*" via "*)
        log "AVISO: $gateway no está directamente conectado; indica PRIVATE_ROUTE_DEV o conecta la red LAN al contenedor"
        return 0
        ;;
    esac
    dev=$(printf '%s\n' "$route" | awk '{for (i = 1; i <= NF; i++) if ($i == "dev") { print $(i + 1); exit }}')
    if [ -z "$dev" ]; then
      log "AVISO: no se pudo detectar la interfaz de $gateway; indica PRIVATE_ROUTE_DEV"
      return 0
    fi
  fi

  routes="${PRIVATE_ROUTES:-10.0.0.0/8 172.16.0.0/12 192.168.0.0/16}"
  routes=$(printf '%s' "$routes" | tr ',' ' ')
  for cidr in $routes; do
    if ip route replace "$cidr" via "$gateway" dev "$dev" 2>/dev/null; then
      log "ruta privada $cidr via $gateway dev $dev"
    else
      log "AVISO: no se pudo añadir la ruta $cidr via $gateway dev $dev"
    fi
  done
}

apply_private_routes

mkdir -p \
  /app/uploads/map-backgrounds \
  /app/uploads/notification-sounds \
  /app/reports \
  /app/certs

exec /usr/bin/supervisord -c /etc/supervisord.conf
