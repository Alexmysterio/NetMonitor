# Dude import: credenciales y enlaces monitorizados

Fecha: 2026-11-06
Estado: aprobado

## Objetivo

Al importar `dude.db` (y, cuando existan, XML o SQLite relacional):

1. Importar las credenciales RouterOS de cada dispositivo.
2. Importar los perfiles SNMP y asignarlos automáticamente a los equipos.
3. Convertir en enlaces SNMP de NetMonitor los enlaces de Dude que tengan
   dispositivo maestro e interfaz, incluyendo los que usan mastering RouterOS.

La importación sigue siendo un preview revisable antes de aplicar cambios.

## Modelo de datos

### Credenciales RouterOS

- Origen: etiquetas Nova `0x1F46` (usuario) y `0x1F47` (contraseña),
  dentro del objeto dispositivo.
- Destino: `routeros_credentials` + `devices.routeros_credential_id`.
- Se agrupan los equipos por pareja usuario/contraseña para no duplicar perfiles.
- Nombre del perfil: `Dude · <nombre del primer equipo>` con sufijo si ya existe
  un perfil con credenciales distintas.
- Por defecto solo se asigna si el equipo no tiene ya credencial RouterOS.
  La casilla "Sobrescribir credenciales RouterOS existentes" permite forzar el cambio
  solo cuando la contraseña importada no está vacía: una contraseña vacía o
  indescifrable nunca sustituye una credencial existente.
- Las credenciales existentes solo se reutilizan si usuario y contraseña
  descifrados coinciden.

### Perfiles SNMP

- Origen Nova: rango `0x3C68–0x3C72`.
  - `0x0010`: nombre.
  - `0x3C68`: versión (0=v1, 1=v2c; otros se tratan como v3 o desconocido).
  - `0x3C69`: community.
  - `0x3C6A`: puerto.
  - `0x3C71`: reintentos.
  - `0x3C72`: timeout en ms.
- El dispositivo referencia su perfil con `0x1F4E`.
- Destino: `snmp_profiles`; nombre `Dude · <nombre>`, reutilizando si
  versión/puerto/community coinciden.
- SNMPv3: se importa la ficha (nombre, versión, puerto) y se avisa de que los
  secretos v3 deben completarse manualmente.
- Una community indescifrable omite el perfil con un aviso.
- La asignación manual del preview (`snmp_profiles`) tiene prioridad sobre la
  asignación automática de Dude. La asignación automática nunca sobrescribe un
  perfil SNMP ya asignado; solo lo hace una selección manual.

### Enlaces monitorizados

- Origen Nova: `0x55F0` mastering, `0x55F1` dispositivo maestro y
  `0x55F2` interfaz maestra.
- Si hay dispositivo maestro importado e interfaz válida, el enlace se crea con:
  - `mastering_type = 'snmp'`
  - `master_device_id = <equipo importado>`
  - `snmp_if_index = <interfaz de Dude>`
- El enlace solo queda en SNMP si el equipo maestro tiene un perfil SNMP
  (recién asignado, manual o preexistente). Si no lo tiene, se crea simple y se
  añade un aviso. El poller ignora los enlaces SNMP sin perfil en el maestro.
- Si el equipo maestro no se importa o no hay interfaz, el enlace se crea simple
  y se genera un aviso.
- Casilla en el preview "Monitorizar enlaces con interfaz", activada por defecto.
- NetMonitor solo soporta SNMP para tráfico; en enlaces con mastering RouterOS
  se usará el índice de interfaz de Dude y se avisará de que puede requerir
  ajuste si no coincide con el ifIndex SNMP.

## Seguridad de los secretos

- El parser devuelve secretos en memoria, nunca en JSON (`json:"-"`).
- Al guardar el preview se cifran contraseña RouterOS y community SNMP con
  `secure.Encrypt`; el payload de `dude_imports` solo contiene ciphertext.
- La API de preview e historial limpia los campos cifrados antes de responder.
- Al aplicar se descifran; si falla el descifrado se omite esa credencial o
  perfil y se añade un aviso. Una contraseña vacía o indescifrable nunca
  sustituye una credencial RouterOS existente.
- No se crea una tabla nueva de secretos.

## Cambios funcionales

- Corregir `readNovaValue` para tratar el marcador `0x11` según su `tcode`
  (Dude lo usa con u32 y strings en campos de continuación).
- Añadir al preview:
  - usuario, `has_password`, perfil SNMP y nombre del perfil por dispositivo;
  - lista de perfiles SNMP detectados;
  - mastering, dispositivo maestro e interfaz por enlace.
- `applyDudeImport` recibe:
  - `import_credentials` (bool, por defecto true)
  - `import_snmp` (bool, por defecto true)
  - `overwrite_credentials` (bool, por defecto false)
  - `assign_link_monitoring` (bool, por defecto true)
- Resultado de la importación:
  - credenciales RouterOS creadas/reutilizadas,
  - perfiles SNMP creados/reutilizados,
  - enlaces monitorizados.

## Interfaz

- Actualizar textos que hoy dicen que no se importan credenciales.
- Añadir un bloque de opciones de importación en el preview.
- Mostrar en cada equipo un indicador de credencial y de perfil SNMP detectado.
- Mostrar cuántos enlaces se van a monitorizar y el resultado final.
- Mantener el selector manual de perfil SNMP como override.

## Pruebas

- Tests unitarios del decoder Nova con marcador `0x11` (strings y u32).
- Test de parseo de un SQLite con dispositivo con credenciales, perfil SNMP y
  enlace con maestro/interfaz.
- Test de las reglas de asignación (sin sobrescribir por defecto, override manual).
- Build de frontend y pruebas Go existentes.
- Prueba manual con un `dude.db` sintético: preview, aplicación, verificación en
  base de datos y enlace SNMP en el mapa.
