#!/bin/sh
set -eu

mkdir -p \
  /app/uploads/map-backgrounds \
  /app/uploads/notification-sounds \
  /app/reports \
  /app/certs

exec /usr/bin/supervisord -c /etc/supervisord.conf
