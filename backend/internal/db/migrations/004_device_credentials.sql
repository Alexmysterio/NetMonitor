-- Per-device credentials for tool access (Winbox, WebFig/HTTP, SSH…).
-- Passwords are encrypted at rest with the same key rotation as the rest of
-- the secrets in NetMonitor. The API never returns the password except through
-- an explicit user action (reveal/copy) or a one-time launch ticket.
CREATE TABLE IF NOT EXISTS device_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind VARCHAR(20) NOT NULL CHECK (kind IN ('winbox','web','ssh','api','other')),
    username VARCHAR(120) NOT NULL DEFAULT '',
    password TEXT NOT NULL DEFAULT '',
    port INT NOT NULL DEFAULT 0,
    use_tls BOOLEAN NOT NULL DEFAULT FALSE,
    insecure_tls BOOLEAN NOT NULL DEFAULT FALSE,
    path TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (device_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_device_credentials_device ON device_credentials(device_id);
