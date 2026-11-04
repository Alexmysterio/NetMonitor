# NetMonitor

> Versión actual: 2026-11-03 · una sola imagen de aplicación (`ghcr.io/alexmysterio/netmonitor:latest`) más PostgreSQL y Redis.

Monitorización de red visual, self-hosted y open source.

NetMonitor es una alternativa web a los clásicos paneles de monitorización de red: mapas jerárquicos en tiempo real, polling multiprotocolo, alertas con supresión por dependencias, traps SNMP, informes y gestión de RouterOS. Está pensado para redes pequeñas y medianas que necesitan una visión clara sin depender de servicios externos.

> Interfaz en español. El stack se ejecuta íntegramente en Docker Compose.

## Características

- **Mapas visuales jerárquicos**: mapas y submapas, zonas, etiquetas dinámicas, enlaces con tráfico RX/TX, modo NOC y edición arrastrando nodos.
- **Descubrimiento de red**: uno o varios rangos CIDR, ICMP + SNMP + TCP, hasta 65.536 direcciones por barrido; crea la sonda según el método de detección, reutiliza equipos existentes y permite añadirlos al mapa.
- **Polling multiprotocolo**: ICMP, SNMP v1/v2c/v3, TCP, HTTP/HTTPS, DNS y sondas lógicas.
- **Inventario SNMP**: sistema, interfaces, direcciones IP, ARP, rutas, LLDP/CDP/MNDP, FDB, Q-BRIDGE, STP y topología L2.
- **Alertas**: reglas por dispositivo/mapa/etiqueta, retardos, reintentos, ventanas de mantenimiento, festivos, antitormenta, dependencias `Parents` y reconocimiento (ACK).
- **Notificaciones**: Telegram interactivo, email, webhooks, Slack, Discord, Teams, ntfy, Syslog, PagerDuty, Opsgenie y SMS.
- **SNMP Traps y Syslog**: recepción, decodificación con MIBs, reglas y feed de eventos.
- **Correlación de causa raíz**: supresión de alertas derivadas de una caída padre o de un fallo de topología L2.
- **RouterOS**: inventario, plantillas, despliegues con aprobación obligatoria, dry-run y snapshots antes/después.
- **Informes**: PDF/CSV, SLA por cliente, disponibilidad por interfaz, comparativa mensual, programación y exportación local/S3.
- **Importación de The Dude**: mapas, dispositivos y enlaces desde SQLite/XML.
- **Seguridad**: RBAC por mapa/grupo/dispositivo, API keys, auditoría, cifrado AES-256-GCM de secretos, JWT revocable y sesiones con refresco de permisos.
- **Agentes remotos**: sondeo desde otras redes, cuotas, reconexión y mTLS opcional.
- **Alta disponibilidad**: elección de líder por advisory lock de PostgreSQL y fanout de WebSocket con Redis.

## Stack

| Componente | Tecnología |
| :--- | :--- |
| Backend, poller y receptores | Go |
| Frontend | React + TypeScript + Vite |
| Base de datos | PostgreSQL 16 + TimescaleDB |
| Estado en vivo y Pub/Sub | Redis 7 |
| Proxy y estáticos | nginx |
| TLS opcional | Caddy |
| Backups | `pg_dump` + sidecar de verificación |

## Inicio rápido

Requisitos:

- Docker Engine 24+ con Compose v2 (Docker Desktop en Windows/macOS).
- 2 GB de RAM recomendados.
- Puertos libres: `8080` (web). Los puertos `162/udp` y `514/udp` solo son necesarios si activas traps y Syslog con el compose avanzado; en Linux los puertos menores de 1024 requieren privilegios.

```bash
git clone https://github.com/Alexmysterio/NetMonitor.git
cd NetMonitor
cp .env.example .env
```

Edita `.env` y define valores aleatorios para `POSTGRES_PASSWORD`, `JWT_SECRET`, `ENCRYPTION_KEY` y `ADMIN_PASSWORD`. Más abajo tienes un `.env` de ejemplo listo para copiar.

Puedes generarlos con OpenSSL:

```bash
openssl rand -hex 32
```

O en PowerShell:

```powershell
$bytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
($bytes | ForEach-Object { $_.ToString('x2') }) -join ''
```

Arranca el stack:

```bash
docker compose up -d
docker compose ps
```

Abre **http://localhost:8080** y entra con el usuario `admin` y la contraseña de `ADMIN_PASSWORD`.

`ADMIN_PASSWORD` se usa únicamente al crear la base de datos. Si el stack ya tenía usuarios, cambia la contraseña desde **Usuarios** o actualiza el hash en la base de datos.

El stack rechaza los valores de ejemplo en producción. Para un laboratorio desechable puedes usar `ALLOW_INSECURE_DEFAULTS=true`, pero nunca en un entorno accesible. `SEED_DEMO=true` crea una topología de demostración solo si la base de datos está vacía; ponlo a `false` en producción.

### Usar la imagen publicada en GHCR

La imagen se publica automáticamente en GitHub Container Registry desde la rama `main`:

- `ghcr.io/alexmysterio/netmonitor:latest` — API, poller, frontend, nginx y binario del agente opcional en una sola imagen.

La imagen se compila para `linux/amd64` y `linux/arm64`.

El compose básico levanta PostgreSQL, Redis y una única imagen de NetMonitor, sin compilar nada. La aplicación (API, poller, frontend y nginx) corre en un solo contenedor; PostgreSQL y Redis se mantienen como servicios oficiales de infraestructura. El contenedor `netmonitor` solo recibe las variables imprescindibles; el resto se activa con el compose avanzado. Puedes copiar el compose tal cual en un archivo `docker-compose.yml`.

### Archivo `.env` de ejemplo

Todas las variables que usa el compose están definidas aquí; no hay valores por defecto ocultos. Cambia los cuatro valores `CAMBIA_ESTO` y ajusta lo que necesites. `POSTGRES_DB` y `POSTGRES_USER` puedes dejarlos así:

```env
POSTGRES_DB=netmonitor
POSTGRES_USER=netmonitor
POSTGRES_PASSWORD=CAMBIA_ESTO

JWT_SECRET=CAMBIA_ESTO
ENCRYPTION_KEY=CAMBIA_ESTO
ADMIN_PASSWORD=CAMBIA_ESTO

HTTP_BIND=127.0.0.1
HTTP_PORT=8080
SEED_DEMO=true
TZ=Europe/Madrid
PUBLIC_URL=http://localhost:8080

# Rutas hacia redes privadas (RFC1918) desde la interfaz LAN del contenedor.
# Deja el gateway vacío para mantener el enrutado por defecto de Docker.
PRIVATE_ROUTE_GATEWAY=
PRIVATE_ROUTE_DEV=
PRIVATE_ROUTES=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16

SNMP_TRAP_PORT=162
SYSLOG_PORT=514
AGENT_MTLS_PORT=8443
POLLER_WORKERS=64
POLL_INTERVAL_SEC=30
ALERT_MAX_PER_MINUTE=120
CORE_LEADER_ELECTION=false

BACKUP_INTERVAL_HOURS=24
BACKUP_RETENTION_DAYS=14
BACKUP_VERIFY=true

DOMAIN=localhost
TLS_EMAIL=internal

AGENT_TOKEN=
AGENT_CORE_WS_URL=ws://netmonitor:8080/api/v1/agent/ws
AGENT_INSECURE_TLS=false
AGENT_TLS_ENABLED=false
AGENT_TLS_SERVER_NAME=netmonitor
AGENT_TLS_CERT=
AGENT_TLS_KEY=
AGENT_TLS_CA=
AGENT_UPDATE_PUBLIC_KEY=
```

Genera cada secreto con `openssl rand -hex 32` o con el comando de PowerShell de más arriba. El archivo completo con opciones adicionales está en [`.env.example`](./.env.example).

Los datos persistentes quedan en `./data/postgres`, `./data/redis`, `./data/backups`, `./map-backgrounds`, `./notification-sounds` y `./reports`; no se usan volúmenes gestionados por Docker.

Si vienes de una versión anterior con volúmenes Docker, copia `networkmonitor_pgdata`, `networkmonitor_redisdata` y `networkmonitor_backups` a `./data` antes de arrancar y no borres los volúmenes antiguos hasta comprobar la migración.

Después guarda este `docker-compose.yml`:

```yaml
services:
  netmonitor-db:
    image: timescale/timescaledb:latest-pg16
    restart: unless-stopped
    environment:
      POSTGRES_DB: ${POSTGRES_DB:?Set POSTGRES_DB in .env}
      POSTGRES_USER: ${POSTGRES_USER:?Set POSTGRES_USER in .env}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?Set POSTGRES_PASSWORD in .env}
      TIMESCALEDB_TELEMETRY: "off"
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
    networks:
      - netmonitor-net
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $$POSTGRES_USER -d $$POSTGRES_DB"]
      interval: 5s
      timeout: 5s
      retries: 20

  netmonitor-redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: ["redis-server", "--appendonly", "yes", "--maxmemory", "512mb", "--maxmemory-policy", "allkeys-lru"]
    volumes:
      - ./data/redis:/data
    networks:
      - netmonitor-net
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 20

  netmonitor:
    image: ghcr.io/alexmysterio/netmonitor:latest
    restart: unless-stopped
    depends_on:
      netmonitor-db:
        condition: service_healthy
      netmonitor-redis:
        condition: service_healthy
    cap_add:
      - NET_RAW
      - NET_ADMIN
    environment:
      APP_ENV: production
      DATABASE_URL: postgres://${POSTGRES_USER:?Set POSTGRES_USER in .env}:${POSTGRES_PASSWORD:?Set POSTGRES_PASSWORD in .env}@netmonitor-db:5432/${POSTGRES_DB:?Set POSTGRES_DB in .env}?sslmode=disable
      REDIS_URL: redis://netmonitor-redis:6379/0
      JWT_SECRET: ${JWT_SECRET:?Set JWT_SECRET in .env}
      ENCRYPTION_KEY: ${ENCRYPTION_KEY:?Set ENCRYPTION_KEY in .env}
      ADMIN_PASSWORD: ${ADMIN_PASSWORD:?Set ADMIN_PASSWORD in .env}
      SEED_DEMO: ${SEED_DEMO:?Set SEED_DEMO in .env}
      PRIVATE_ROUTE_GATEWAY: ${PRIVATE_ROUTE_GATEWAY?Set PRIVATE_ROUTE_GATEWAY in .env (empty disables)}
      PRIVATE_ROUTE_DEV: ${PRIVATE_ROUTE_DEV?Set PRIVATE_ROUTE_DEV in .env (empty autodetects)}
      PRIVATE_ROUTES: ${PRIVATE_ROUTES?Set PRIVATE_ROUTES in .env}
    ports:
      - "${HTTP_BIND:?Set HTTP_BIND in .env}:${HTTP_PORT:?Set HTTP_PORT in .env}:80"
    volumes:
      - ./map-backgrounds:/app/uploads/map-backgrounds
      - ./notification-sounds:/app/uploads/notification-sounds
      - ./reports:/app/reports
    networks:
      - netmonitor-net
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1/api/v1/health"]
      interval: 10s
      timeout: 3s
      retries: 5
      start_period: 15s

networks:
  netmonitor-net:
    driver: bridge
```

Arranca:

```bash
docker compose up -d
```

Si guardas el archivo con otro nombre, usa `docker compose -f <archivo> up -d`.

El mismo archivo está versionado en [`docker-compose.yml`](./docker-compose.yml).

Para descargar la imagen principal por separado:

```bash
docker pull ghcr.io/alexmysterio/netmonitor:latest
```

La imagen `netmonitor:latest` está publicada como pública: no necesitas iniciar sesión en GHCR para descargarla.

### Rutas a redes privadas

Por defecto el contenedor solo conoce la red bridge de Docker y usa su gateway para todo. Si la LAN/VPN está en otra interfaz, NetMonitor puede enviar el tráfico RFC1918 por el gateway de esa interfaz. La configuración no depende de la IP del contenedor: puede ser fija o asignada por Docker, y la interfaz se detecta automáticamente.

**1. Adjunta el contenedor a la red de la LAN/VPN.** Si ya la tienes en tu compose, no hagas nada. Si no, usa el overlay [`docker-compose.lan.yml`](./docker-compose.lan.yml), que adjunta una red externa por nombre sin fijar ninguna IP:

```bash
# Ejemplo con macvlan en Linux; ajusta subred, gateway e interfaz del host
docker network create -d macvlan \
  --subnet=<LAN_CIDR> \
  --gateway=<LAN_GATEWAY> \
  -o parent=<HOST_IF> netmonitor-lan
```

También sirve una red bridge si el host ya enruta hacia esa red:

```bash
docker network create \
  --subnet=<LAN_CIDR> \
  --gateway=<LAN_GATEWAY> \
  netmonitor-lan
```

**2. Configura el gateway en `.env`:**

```env
PRIVATE_ROUTE_GATEWAY=<LAN_GATEWAY>
PRIVATE_ROUTE_DEV=
PRIVATE_ROUTES=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
LAN_NETWORK=netmonitor-lan
```

- `PRIVATE_ROUTE_GATEWAY`: gateway de la red LAN/VPN. Si se deja vacío, las rutas se desactivan y todo sigue saliendo por Docker.
- `PRIVATE_ROUTE_DEV`: interfaz del contenedor (`eth0`, `eth1`...). Si se deja vacío, se detecta desde la ruta directa al gateway.
- `PRIVATE_ROUTES`: CIDR separados por comas o espacios. Por defecto, las tres redes privadas RFC1918.
- `LAN_NETWORK`: solo si usas el overlay; es el nombre de la red externa ya existente.

**3. Arranca:**

```bash
docker compose -f docker-compose.yml -f docker-compose.lan.yml up -d
```

Si ya adjuntaste la red en tu propio compose, usa `docker compose up -d`.

El contenedor necesita `NET_ADMIN` (ya está en `docker-compose.yml`) para aplicar las rutas. La ruta por defecto a Internet no cambia: sigue saliendo por el gateway de Docker. Las rutas propias de Docker (`172.x.0.0/16`, DNS, PostgreSQL, Redis) son más específicas y siguen funcionando. El overlay usa `gw_priority` para mantener el gateway de Docker como predeterminado; requiere una versión reciente de Docker. Si tu versión no lo soporta, el gateway de la LAN podría pasar a ser el predeterminado.

**Comprobar:**

```bash
docker compose exec netmonitor ip route get <IP_PRIVADA>
docker compose exec netmonitor ip route get 1.1.1.1
```

La primera debe mostrar `via <LAN_GATEWAY> dev <interfaz>`; la segunda, el gateway de Docker. Si aparece `Operation not permitted`, falta `NET_ADMIN`; si aparece un aviso de gateway no conectado, el contenedor no está unido a la red de ese gateway.

El compose básico no incluye backups, agente remoto, traps/Syslog publicados, iconos personalizados ni TLS/mTLS. Esas piezas están en archivos opcionales para que cada usuario elija. El overlay principal es [`docker-compose.advanced.yml`](./docker-compose.advanced.yml):

```bash
# Añade traps, Syslog, certificados e iconos personalizados
docker compose -f docker-compose.yml -f docker-compose.advanced.yml up -d

# Añade además el sidecar de backups
docker compose -f docker-compose.yml -f docker-compose.advanced.yml --profile backup up -d

# Añade además un agente remoto
docker compose -f docker-compose.yml -f docker-compose.advanced.yml --profile agent up -d

# HTTPS con Caddy
docker compose -f docker-compose.yml -f docker-compose.advanced.yml -f docker-compose.tls.yml --profile tls up -d
```

## Configuración

Toda la configuración se pasa por variables de entorno. Las principales:

| Variable | Descripción | Valor por defecto |
| :--- | :--- | :--- |
| `HTTP_BIND` | Interfaz donde se publica la web | `127.0.0.1` |
| `HTTP_PORT` | Puerto HTTP publicado | `8080` |
| `PRIVATE_ROUTE_GATEWAY` | Gateway LAN/VPN para el tráfico RFC1918; vacío lo desactiva | Vacío |
| `PRIVATE_ROUTE_DEV` | Interfaz del contenedor; vacío la autodetecta | Vacío |
| `PRIVATE_ROUTES` | CIDR privados que usan ese gateway | `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16` |
| `LAN_NETWORK` | Red externa para el overlay `docker-compose.lan.yml`; sin IP fija | Vacío |
| `POSTGRES_PASSWORD` | Contraseña de PostgreSQL | Cambiar obligatoriamente |
| `JWT_SECRET` | Firma de sesiones JWT | Cambiar obligatoriamente |
| `ENCRYPTION_KEY` | Clave de cifrado en reposo | Cambiar obligatoriamente |
| `ADMIN_USERNAME` | Usuario inicial | `admin` |
| `ADMIN_PASSWORD` | Contraseña inicial | Cambiar obligatoriamente |
| `SEED_DEMO` | Crea datos de demostración al inicio | `true` |
| `PUBLIC_URL` | URL pública para botones de Telegram (avanzado) | `http://localhost:8080` |
| `SNMP_TRAP_PORT` | Puerto UDP de traps en el host (avanzado) | `162` |
| `SYSLOG_PORT` | Puerto UDP de Syslog en el host (avanzado) | `514` |
| `BACKUP_INTERVAL_HOURS` | Frecuencia de backup (avanzado) | `24` |
| `BACKUP_RETENTION_DAYS` | Retención de backups (avanzado) | `14` |
| `CORE_LEADER_ELECTION` | Activa elección de líder para HA (avanzado) | `false` |

El resto de opciones está documentado en `.env.example`. Las variables de ajuste `TZ`, `PUBLIC_URL`, `POLLER_WORKERS`, `POLL_INTERVAL_SEC`, `ALERT_MAX_PER_MINUTE` y `CORE_LEADER_ELECTION` se aplican con `docker-compose.advanced.yml`.

## Operación

```bash
# Estado y logs
docker compose ps
docker compose logs -f netmonitor

# Salud de la API
curl http://localhost:8080/api/v1/health

# Backup manual (requiere el overlay avanzado y el perfil backup)
docker compose -f docker-compose.yml -f docker-compose.advanced.yml --profile backup run --rm netmonitor-backup once

# Actualizar imágenes
docker compose pull
docker compose up -d
```

Los backups se guardan en `./data/backups` (`/backups` dentro del contenedor) y se verifica su restauración en una base temporal.

### Rotación de `ENCRYPTION_KEY`

La rotación vuelve a cifrar credenciales SNMP, credenciales RouterOS, canales de notificación y almacenamiento de informes. Haz un backup antes y ejecuta el rotador con el contenedor parado:

```bash
docker compose stop netmonitor

docker compose run --rm --entrypoint /app/netmonitor-keyrotate \
  -e OLD_ENCRYPTION_KEY='clave_actual' \
  -e NEW_ENCRYPTION_KEY='clave_nueva' \
  netmonitor
```

Actualiza `ENCRYPTION_KEY` en `.env` y vuelve a arrancar:

```bash
docker compose up -d netmonitor
```

Si algún secreto no se puede descifrar con la clave anterior, el proceso aborta sin dejar la base de datos a medias.

## HTTPS

Para un dominio público con certificado automático de Caddy:

```powershell
$env:DOMAIN="monitor.example.com"
$env:TLS_EMAIL="ops@example.com"
docker compose -f docker-compose.yml -f docker-compose.tls.yml --profile tls up -d
```

Para un certificado local autofirmado usa `TLS_EMAIL=internal` y abre `https://localhost`.

## Agentes remotos

El agente remoto reutiliza la misma imagen `netmonitor`: el overlay avanzado cambia el `entrypoint` a `/app/netmonitor-agent`.

Crea un agente en **Ajustes → Agentes**, copia el token y arranca el perfil:

```powershell
$env:AGENT_TOKEN="tu_token"
docker compose -f docker-compose.yml -f docker-compose.advanced.yml --profile agent up -d
```

Para mTLS, genera los certificados con:

```powershell
cd backend
go run ./cmd/certgen -out ..\deploy\certs -hosts netmonitor,localhost -ips 127.0.0.1
```

y activa `AGENT_TLS_ENABLED=true` en `.env`. El puerto `8443` exige certificado de cliente firmado por la CA. Si usas mTLS, configura también `AGENT_CORE_WS_URL=wss://netmonitor:8443/api/v1/agent/ws` y descomenta `AGENT_TLS_CERT`, `AGENT_TLS_KEY` y `AGENT_TLS_CA`.

## Desarrollo

Requiere Go 1.23+ y Node.js 20+.

```bash
# Imagen all-in-one
docker build -f deploy/allinone/Dockerfile -t netmonitor:dev .

# Backend
cd backend
go build ./...
go vet ./...
go test ./...

# Frontend
cd ../frontend
npm ci
npm run build
```

La especificación OpenAPI está disponible en `/api/v1/openapi.json` con el stack arrancado.

## Seguridad

- No subas `.env` ni `deploy/certs/` al repositorio. Ya están excluidos en `.gitignore`.
- Usa HTTPS si el panel es accesible desde fuera de la red local.
- Mantén `ALLOW_INSECURE_DEFAULTS=false` en producción.
- Si usas el compose avanzado, restringe los puertos `162/udp` y `514/udp` a la red de gestión.
- Crea API keys con el rol mínimo necesario y rótalas periódicamente.
- Los despliegues RouterOS requieren aprobación; con más de un administrador, el creador no puede aprobar su propio trabajo.
- Rota en origen las credenciales externas (Telegram, SNMP, S3) si alguna vez pudieron quedar expuestas.

## Licencia

Distribuido bajo licencia **MIT**. Consulta [`LICENSE`](./LICENSE).

Los iconos de topología Cisco incluidos en `frontend/src/assets/cisco/` son marcas de Cisco Systems, Inc. y se redistribuyen sin modificaciones únicamente para identificación de dispositivos. No están cubiertos por la licencia MIT. Consulta [`THIRD_PARTY_NOTICES.md`](./THIRD_PARTY_NOTICES.md).

## Contribuir

Las issues y pull requests son bienvenidas. Antes de enviar cambios:

1. Ejecuta `go test ./...` y `npm run build`.
2. No incluyas secretos, certificados, backups ni datos reales de clientes.
3. Mantén el estilo y los mensajes existentes.

## Estado

Proyecto en desarrollo activo. Las funciones principales de monitorización, mapas, alertas, traps, RouterOS e informes están implementadas y verificadas sobre Docker Compose. Las limitaciones conocidas se irán documentando en las issues del repositorio.
