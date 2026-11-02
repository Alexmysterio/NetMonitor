-- Fase 4 · Correcciones de seguridad, consistencia y operación.

-- Revocación de sesiones y refresco de permisos.
ALTER TABLE users ADD COLUMN IF NOT EXISTS token_version INT NOT NULL DEFAULT 0;

-- Histéresis de recuperación de sondas.
ALTER TABLE device_services ADD COLUMN IF NOT EXISTS current_up_count INT NOT NULL DEFAULT 0;

-- Claim atómico de ejecuciones de polling entre workers e instancias.
CREATE TABLE IF NOT EXISTS poll_in_flight (
    service_id UUID PRIMARY KEY REFERENCES device_services(id) ON DELETE CASCADE,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_poll_in_flight_expiry ON poll_in_flight(expires_at);

-- Evita dos despliegues RouterOS simultáneos sobre el mismo equipo.
CREATE TABLE IF NOT EXISTS routeros_device_locks (
    device_id UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    job_id UUID NOT NULL REFERENCES routeros_jobs(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_routeros_device_locks_expiry ON routeros_device_locks(expires_at);

-- Una sola incidencia activa por dispositivo.
WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY device_id ORDER BY started_at, id) AS rn
    FROM outages WHERE ended_at IS NULL
)
UPDATE outages SET ended_at = NOW()
WHERE id IN (SELECT id FROM ranked WHERE rn > 1);
CREATE UNIQUE INDEX IF NOT EXISTS idx_outages_one_active
    ON outages(device_id) WHERE ended_at IS NULL;

-- Deduplicar elementos de dispositivo antes de impedir duplicados nuevos.
WITH duplicates AS (
    SELECT id,
           first_value(id) OVER (PARTITION BY map_id, device_id ORDER BY created_at, id) AS keeper_id,
           row_number() OVER (PARTITION BY map_id, device_id ORDER BY created_at, id) AS rn
    FROM map_elements
    WHERE element_type='device' AND device_id IS NOT NULL
)
UPDATE map_links ml
SET source_element_id = d.keeper_id
FROM duplicates d
WHERE d.rn > 1 AND ml.source_element_id = d.id;

WITH duplicates AS (
    SELECT id,
           first_value(id) OVER (PARTITION BY map_id, device_id ORDER BY created_at, id) AS keeper_id,
           row_number() OVER (PARTITION BY map_id, device_id ORDER BY created_at, id) AS rn
    FROM map_elements
    WHERE element_type='device' AND device_id IS NOT NULL
)
UPDATE map_links ml
SET target_element_id = d.keeper_id
FROM duplicates d
WHERE d.rn > 1 AND ml.target_element_id = d.id;

WITH duplicates AS (
    SELECT id,
           row_number() OVER (PARTITION BY map_id, device_id ORDER BY created_at, id) AS rn
    FROM map_elements
    WHERE element_type='device' AND device_id IS NOT NULL
)
DELETE FROM map_elements e
USING duplicates d
WHERE e.id = d.id AND d.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_map_elements_unique_device
    ON map_elements(map_id, device_id)
    WHERE element_type='device' AND device_id IS NOT NULL;

-- Índices de consulta para las nuevas pantallas.
CREATE INDEX IF NOT EXISTS idx_report_runs_generated_by ON report_runs(generated_by, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dude_imports_status ON dude_imports(status, created_at DESC);
