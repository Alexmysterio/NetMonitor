#!/bin/sh
set -eu

: "${PGHOST:=db}"
: "${PGUSER:=netmonitor}"
: "${PGDATABASE:=netmonitor}"
: "${BACKUP_DIR:=/backups}"
: "${BACKUP_INTERVAL_HOURS:=24}"
: "${BACKUP_RETENTION_DAYS:=14}"
: "${BACKUP_VERIFY:=true}"

mkdir -p "$BACKUP_DIR"
export PGPASSWORD="${PGPASSWORD:-}"

log() { echo "[backup] $(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

verify_dump() {
  file="$1"
  pg_restore --list "$file" >/dev/null
  if [ "$BACKUP_VERIFY" = "true" ]; then
    checkdb="${PGDATABASE}_verify_$(date -u +%s)_$$"
    log "verificando restauración en $checkdb"
    dropdb --if-exists "$checkdb" 2>/dev/null || true
    createdb "$checkdb"
    if ! pg_restore --no-owner --no-privileges --exit-on-error --single-transaction -d "$checkdb" "$file"; then
      dropdb --if-exists "$checkdb" 2>/dev/null || true
      log "ERROR: la restauración de verificación falló"
      return 1
    fi
    tables=$(psql -d "$checkdb" -tAc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")
    devices=$(psql -d "$checkdb" -tAc "SELECT count(*) FROM devices")
    maps=$(psql -d "$checkdb" -tAc "SELECT count(*) FROM maps")
    dropdb --if-exists "$checkdb" 2>/dev/null || true
    log "restauración verificada: $tables tablas, $devices dispositivos, $maps mapas"
  fi
}

run_backup() {
  ts=$(date -u +%Y%m%dT%H%M%SZ)
  file="$BACKUP_DIR/netmonitor-$ts.dump"
  tmp="$file.tmp"
  log "iniciando backup $file"
  if ! pg_dump -Fc -f "$tmp"; then
    rm -f "$tmp"
    log "ERROR: pg_dump falló"
    return 1
  fi
  mv "$tmp" "$file"
  verify_dump "$file"
  log "backup completado: $(du -h "$file" | cut -f1)"
  find "$BACKUP_DIR" -name 'netmonitor-*.dump' -type f -mtime +"$BACKUP_RETENTION_DAYS" -delete
}

if [ "${1:-}" = "once" ] || [ "${BACKUP_ONCE:-}" = "1" ]; then
  run_backup
  exit $?
fi

while true; do
  run_backup || log "el backup falló; se reintentará en el siguiente ciclo"
  sleep "$((BACKUP_INTERVAL_HOURS * 3600))"
done
