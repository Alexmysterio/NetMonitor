CREATE EXTENSION IF NOT EXISTS timescaledb;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(80) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name VARCHAR(120) NOT NULL DEFAULT '',
    role VARCHAR(20) NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'operator', 'viewer')),
    restrict_maps BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS snmp_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    version VARCHAR(10) NOT NULL DEFAULT 'v2c' CHECK (version IN ('v1', 'v2c', 'v3')),
    port INT NOT NULL DEFAULT 161,
    timeout_ms INT NOT NULL DEFAULT 1500,
    retries INT NOT NULL DEFAULT 2,
    community TEXT NOT NULL DEFAULT 'public',
    v3_username VARCHAR(120),
    v3_security_level VARCHAR(20) DEFAULT 'authPriv',
    v3_auth_protocol VARCHAR(20) DEFAULT 'SHA',
    v3_auth_pass TEXT,
    v3_priv_protocol VARCHAR(20) DEFAULT 'AES',
    v3_priv_pass TEXT,
    v3_context_name VARCHAR(120) DEFAULT '',
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL UNIQUE,
    token_hash VARCHAR(128) NOT NULL,
    version VARCHAR(60) NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ,
    max_devices INT NOT NULL DEFAULT 0,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agents_token ON agents(token_hash);
ALTER TABLE agents ADD COLUMN IF NOT EXISTS max_devices INT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS maps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_map_id UUID REFERENCES maps(id) ON DELETE SET NULL,
    name VARCHAR(120) NOT NULL,
    slug VARCHAR(120) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    background_color VARCHAR(20) NOT NULL DEFAULT '#FFFFFF',
    background_image_url TEXT,
    background_image_opacity NUMERIC(3,2) NOT NULL DEFAULT 1.0,
    grid_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    grid_size INT NOT NULL DEFAULT 16,
    snap_to_grid BOOLEAN NOT NULL DEFAULT TRUE,
    status_aggregation_mode VARCHAR(30) NOT NULL DEFAULT 'worst',
    default_device_shape VARCHAR(20) NOT NULL DEFAULT 'circle',
    label_align VARCHAR(10) NOT NULL DEFAULT 'left' CHECK (label_align IN ('left','center','right')),
    background_image VARCHAR(500) NOT NULL DEFAULT '',
    background_opacity NUMERIC(3,2) NOT NULL DEFAULT 1,
    default_device_label TEXT NOT NULL DEFAULT '[Device.Name]\n[Device.FirstAddress]',
    viewport JSONB NOT NULL DEFAULT '{"x":0,"y":0,"zoom":1}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(150) NOT NULL,
    primary_ip INET NOT NULL,
    secondary_ips INET[] NOT NULL DEFAULT '{}',
    dns_name VARCHAR(255),
    mac_address VARCHAR(32),
    vendor VARCHAR(120),
    device_type VARCHAR(60) NOT NULL DEFAULT 'generic',
    snmp_profile_id UUID REFERENCES snmp_profiles(id) ON DELETE SET NULL,
    custom_polling_interval_sec INT,
    snmp_system_refresh_sec INT,
    snmp_interfaces_refresh_sec INT,
    snmp_ips_refresh_sec INT,
    snmp_arp_refresh_sec INT,
    snmp_routes_refresh_sec INT,
    sys_name VARCHAR(255),
    sys_descr TEXT,
    sys_object_id VARCHAR(120),
    sys_location VARCHAR(255),
    sys_contact VARCHAR(255),
    hardware_model VARCHAR(150),
    routeros_version VARCHAR(80),
    custom_fields JSONB NOT NULL DEFAULT '{}',
    notes TEXT NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'unknown' CHECK (status IN ('up','warning','down','unreachable','acknowledged','disabled','unknown')),
    status_reason TEXT NOT NULL DEFAULT '',
    last_status_change_at TIMESTAMPTZ,
    last_seen_up_at TIMESTAMPTZ,
    is_acknowledged BOOLEAN NOT NULL DEFAULT FALSE,
    ack_by VARCHAR(120),
    ack_note TEXT,
    ack_expires_at TIMESTAMPTZ,
    maintenance_until TIMESTAMPTZ,
    maintenance_note TEXT,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    is_monitoring_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    tags TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE devices ADD COLUMN IF NOT EXISTS snmp_system_refresh_sec INT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS snmp_interfaces_refresh_sec INT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS snmp_ips_refresh_sec INT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS snmp_arp_refresh_sec INT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS snmp_routes_refresh_sec INT;
ALTER TABLE devices ALTER COLUMN snmp_system_refresh_sec DROP NOT NULL;
ALTER TABLE devices ALTER COLUMN snmp_system_refresh_sec DROP DEFAULT;
ALTER TABLE devices ALTER COLUMN snmp_interfaces_refresh_sec DROP NOT NULL;
ALTER TABLE devices ALTER COLUMN snmp_interfaces_refresh_sec DROP DEFAULT;
ALTER TABLE devices ALTER COLUMN snmp_ips_refresh_sec DROP NOT NULL;
ALTER TABLE devices ALTER COLUMN snmp_ips_refresh_sec DROP DEFAULT;
ALTER TABLE devices ALTER COLUMN snmp_arp_refresh_sec DROP NOT NULL;
ALTER TABLE devices ALTER COLUMN snmp_arp_refresh_sec DROP DEFAULT;
ALTER TABLE devices ALTER COLUMN snmp_routes_refresh_sec DROP NOT NULL;
ALTER TABLE devices ALTER COLUMN snmp_routes_refresh_sec DROP DEFAULT;

CREATE TABLE IF NOT EXISTS map_permissions (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    map_id UUID NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, map_id)
);

CREATE INDEX IF NOT EXISTS idx_map_permissions_user ON map_permissions(user_id);

ALTER TABLE users ADD COLUMN IF NOT EXISTS restrict_maps BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS maintenance_until TIMESTAMPTZ;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS maintenance_note TEXT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS agent_id UUID REFERENCES agents(id) ON DELETE SET NULL;
ALTER TABLE users ADD COLUMN IF NOT EXISTS restrict_groups BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS restrict_devices BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS allowed_probe_types TEXT[] NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS user_device_permissions (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, device_id)
);

CREATE TABLE IF NOT EXISTS device_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    color VARCHAR(20) NOT NULL DEFAULT '#2563eb',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS device_group_members (
    group_id UUID NOT NULL REFERENCES device_groups(id) ON DELETE CASCADE,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, device_id)
);

CREATE TABLE IF NOT EXISTS user_device_groups (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id UUID NOT NULL REFERENCES device_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_group_members_device ON device_group_members(device_id);
CREATE INDEX IF NOT EXISTS idx_user_groups_user ON user_device_groups(user_id);

CREATE INDEX IF NOT EXISTS idx_devices_status ON devices(status);
CREATE INDEX IF NOT EXISTS idx_devices_agent ON devices(agent_id);
CREATE INDEX IF NOT EXISTS idx_devices_primary_ip ON devices(primary_ip);
CREATE INDEX IF NOT EXISTS idx_devices_tags ON devices USING GIN(tags);

CREATE TABLE IF NOT EXISTS device_parents (
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    parent_device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    PRIMARY KEY (device_id, parent_device_id),
    CHECK (device_id <> parent_device_id)
);

CREATE TABLE IF NOT EXISTS map_elements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    map_id UUID NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    element_type VARCHAR(20) NOT NULL DEFAULT 'device' CHECK (element_type IN ('device','submap','network','zone','text_label')),
    device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
    target_submap_id UUID REFERENCES maps(id) ON DELETE CASCADE,
    network_cidr CIDR,
    pos_x NUMERIC(10,2) NOT NULL DEFAULT 0,
    pos_y NUMERIC(10,2) NOT NULL DEFAULT 0,
    width NUMERIC(8,2) NOT NULL DEFAULT 180,
    height NUMERIC(8,2) NOT NULL DEFAULT 88,
    rotation NUMERIC(6,2) NOT NULL DEFAULT 0,
    z_index INT NOT NULL DEFAULT 10,
    zone_id UUID REFERENCES map_elements(id) ON DELETE SET NULL,
    is_locked BOOLEAN NOT NULL DEFAULT FALSE,
    shape VARCHAR(20) NOT NULL DEFAULT 'rounded_rect' CHECK (shape IN ('circle','square','rounded_rect','pill','diamond','hexagon')),
    icon_name VARCHAR(60) NOT NULL DEFAULT 'server',
    show_icon BOOLEAN NOT NULL DEFAULT TRUE,
    label_template TEXT NOT NULL DEFAULT '[Device.Name]\n[Device.FirstAddress]',
    label_position VARCHAR(20) NOT NULL DEFAULT 'inside' CHECK (label_position IN ('inside','bottom','top','right')),
    custom_style JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_map_elements_map ON map_elements(map_id);
CREATE INDEX IF NOT EXISTS idx_map_elements_device ON map_elements(device_id);
ALTER TABLE map_elements ADD COLUMN IF NOT EXISTS rotation NUMERIC(6,2) NOT NULL DEFAULT 0;
ALTER TABLE map_elements ADD COLUMN IF NOT EXISTS zone_id UUID REFERENCES map_elements(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_map_elements_zone ON map_elements(zone_id);

CREATE TABLE IF NOT EXISTS map_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    map_id UUID NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    source_element_id UUID NOT NULL REFERENCES map_elements(id) ON DELETE CASCADE,
    target_element_id UUID NOT NULL REFERENCES map_elements(id) ON DELETE CASCADE,
    source_handle VARCHAR(30),
    target_handle VARCHAR(30),
    mastering_type VARCHAR(20) NOT NULL DEFAULT 'simple' CHECK (mastering_type IN ('simple','snmp')),
    master_device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    snmp_if_index INT,
    snmp_if_name VARCHAR(120),
    link_type VARCHAR(40) NOT NULL DEFAULT 'ethernet',
    max_speed_bps BIGINT NOT NULL DEFAULT 1000000000,
    warning_utilization_pct NUMERIC(5,2) NOT NULL DEFAULT 75,
    critical_utilization_pct NUMERIC(5,2) NOT NULL DEFAULT 90,
    color_by_traffic BOOLEAN NOT NULL DEFAULT TRUE,
    routing_style VARCHAR(20) NOT NULL DEFAULT 'straight' CHECK (routing_style IN ('straight','orthogonal','bezier')),
    thickness NUMERIC(4,1) NOT NULL DEFAULT 2.5,
    dashed BOOLEAN NOT NULL DEFAULT FALSE,
    label_template TEXT NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'up',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_map_links_map ON map_links(map_id);
ALTER TABLE map_links ADD COLUMN IF NOT EXISTS color_by_traffic BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE maps ALTER COLUMN default_device_shape SET DEFAULT 'circle';
ALTER TABLE maps ADD COLUMN IF NOT EXISTS label_align VARCHAR(10) NOT NULL DEFAULT 'left';
ALTER TABLE maps ADD COLUMN IF NOT EXISTS background_image VARCHAR(500) NOT NULL DEFAULT '';
ALTER TABLE maps ADD COLUMN IF NOT EXISTS background_opacity NUMERIC(3,2) NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS probes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    probe_type VARCHAR(20) NOT NULL CHECK (probe_type IN ('icmp','snmp','tcp','http','dns','function','logic')),
    description TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}',
    warning_expression TEXT NOT NULL DEFAULT '',
    error_expression TEXT NOT NULL DEFAULT '',
    value_expression TEXT NOT NULL DEFAULT '',
    unit VARCHAR(20) NOT NULL DEFAULT '',
    default_interval_sec INT NOT NULL DEFAULT 30,
    default_timeout_ms INT NOT NULL DEFAULT 2000,
    default_down_retries INT NOT NULL DEFAULT 3,
    default_up_retries INT NOT NULL DEFAULT 1,
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS functions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    expression_code TEXT NOT NULL,
    unit VARCHAR(20) NOT NULL DEFAULT '',
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS device_services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    probe_id UUID NOT NULL REFERENCES probes(id) ON DELETE RESTRICT,
    custom_name VARCHAR(120) NOT NULL DEFAULT '',
    config_overrides JSONB NOT NULL DEFAULT '{}',
    interval_sec INT NOT NULL DEFAULT 30,
    timeout_ms INT NOT NULL DEFAULT 2000,
    down_retries INT NOT NULL DEFAULT 3,
    up_retries INT NOT NULL DEFAULT 1,
    affects_device_status BOOLEAN NOT NULL DEFAULT TRUE,
    is_critical BOOLEAN NOT NULL DEFAULT TRUE,
    status VARCHAR(20) NOT NULL DEFAULT 'unknown',
    current_retry_count INT NOT NULL DEFAULT 0,
    last_value NUMERIC(18,4),
    last_output_message TEXT NOT NULL DEFAULT '',
    last_check_at TIMESTAMPTZ,
    last_status_change_at TIMESTAMPTZ,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (device_id, probe_id, custom_name)
);

CREATE INDEX IF NOT EXISTS idx_device_services_due ON device_services(last_check_at) WHERE is_enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_device_services_device ON device_services(device_id);

CREATE TABLE IF NOT EXISTS outages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    device_service_id UUID REFERENCES device_services(id) ON DELETE SET NULL,
    root_cause_device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    severity VARCHAR(20) NOT NULL DEFAULT 'down',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at TIMESTAMPTZ,
    error_message TEXT NOT NULL DEFAULT '',
    acknowledged BOOLEAN NOT NULL DEFAULT FALSE,
    acknowledged_by VARCHAR(120),
    acknowledged_at TIMESTAMPTZ,
    resolution_note TEXT
);

CREATE INDEX IF NOT EXISTS idx_outages_active ON outages(device_id, started_at DESC) WHERE ended_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_outages_started ON outages(started_at DESC);

CREATE TABLE IF NOT EXISTS notification_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    channel_type VARCHAR(30) NOT NULL CHECK (channel_type IN ('telegram','email','webhook','slack','discord','teams','syslog','web_audio','ntfy')),
    config JSONB NOT NULL DEFAULT '{}',
    title_template TEXT NOT NULL DEFAULT '[{{Status}}] {{Device.Name}}',
    body_template TEXT NOT NULL DEFAULT '{{Device.Name}} ({{Device.FirstAddress}}) - {{Event.Message}}',
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS alert_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL,
    priority INT NOT NULL DEFAULT 100,
    filter_map_ids UUID[] NOT NULL DEFAULT '{}',
    filter_device_ids UUID[] NOT NULL DEFAULT '{}',
    filter_tags TEXT[] NOT NULL DEFAULT '{}',
    filter_probe_ids UUID[] NOT NULL DEFAULT '{}',
    trigger_transitions TEXT[] NOT NULL DEFAULT '{down,recovered}',
    active_schedule JSONB NOT NULL DEFAULT '{"always":true}',
    delay_sec INT NOT NULL DEFAULT 0,
    repeat_interval_sec INT NOT NULL DEFAULT 0,
    max_repeats INT NOT NULL DEFAULT 3,
    channel_ids UUID[] NOT NULL DEFAULT '{}',
    group_by VARCHAR(20) NOT NULL DEFAULT 'none',
    group_window_sec INT NOT NULL DEFAULT 60,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS group_by VARCHAR(20) NOT NULL DEFAULT 'none';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS group_window_sec INT NOT NULL DEFAULT 60;

CREATE TABLE IF NOT EXISTS mib_objects (
    module VARCHAR(120) NOT NULL DEFAULT '',
    oid TEXT NOT NULL,
    name VARCHAR(160) NOT NULL,
    syntax TEXT NOT NULL DEFAULT '',
    access VARCHAR(60) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    imported_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (module, oid)
);

CREATE INDEX IF NOT EXISTS idx_mib_objects_oid ON mib_objects(oid text_pattern_ops);
CREATE INDEX IF NOT EXISTS idx_mib_objects_name ON mib_objects(name);

CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGSERIAL PRIMARY KEY,
    at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id UUID,
    username VARCHAR(120) NOT NULL DEFAULT '',
    ip VARCHAR(80) NOT NULL DEFAULT '',
    method VARCHAR(10) NOT NULL,
    path TEXT NOT NULL,
    status INT NOT NULL DEFAULT 0,
    resource_type VARCHAR(40) NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    details JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_at ON audit_logs(at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user ON audit_logs(username);

CREATE TABLE IF NOT EXISTS api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL,
    key_hash CHAR(64) NOT NULL UNIQUE,
    role VARCHAR(20) NOT NULL DEFAULT 'viewer',
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);

CREATE TABLE IF NOT EXISTS maintenance_windows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL,
    scope_type VARCHAR(10) NOT NULL CHECK (scope_type IN ('device','map','tag')),
    scope_id UUID,
    scope_tag TEXT NOT NULL DEFAULT '',
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(120) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_maintenance_window ON maintenance_windows(starts_at, ends_at);

CREATE TABLE IF NOT EXISTS system_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS device_types (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(60) NOT NULL UNIQUE,
    label VARCHAR(120) NOT NULL,
    icon VARCHAR(60) NOT NULL DEFAULT '',
    shape VARCHAR(30) NOT NULL DEFAULT 'rounded_rect',
    color VARCHAR(20) NOT NULL DEFAULT '#2563eb',
    description TEXT NOT NULL DEFAULT '',
    probe_ids UUID[] NOT NULL DEFAULT '{}',
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO device_types (name, label, icon, shape, color, is_builtin) VALUES
 ('router','Router','router','rounded_rect','#2563eb',TRUE),
 ('switch','Switch','switch','square','#0891b2',TRUE),
 ('server','Servidor','server','rounded_rect','#7c3aed',TRUE),
 ('access_point','Punto de acceso','wifi','circle','#059669',TRUE),
 ('firewall','Firewall','shield','hexagon','#dc2626',TRUE),
 ('printer','Impresora','printer','rounded_rect','#64748b',TRUE),
 ('ups','SAI / UPS','battery','rounded_rect','#d97706',TRUE),
 ('cpe','CPE','radio','pill','#0d9488',TRUE),
 ('camera','Cámara IP','camera','circle','#4f46e5',TRUE),
 ('generic','Genérico','box','rounded_rect','#94a3b8',TRUE)
ON CONFLICT (name) DO NOTHING;

ALTER TABLE device_types ADD COLUMN IF NOT EXISTS probe_ids UUID[] NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS agent_releases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    version VARCHAR(40) NOT NULL,
    channel VARCHAR(10) NOT NULL DEFAULT 'stable',
    url TEXT NOT NULL,
    sha256 CHAR(64) NOT NULL,
    signature TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (version, channel)
);

CREATE TABLE IF NOT EXISTS digest_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL,
    interval_sec INT NOT NULL DEFAULT 3600,
    channel_ids UUID[] NOT NULL DEFAULT '{}',
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS events (
    id BIGSERIAL PRIMARY KEY,
    at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    device_id UUID,
    map_id UUID,
    severity VARCHAR(20) NOT NULL DEFAULT 'info',
    event_type VARCHAR(50) NOT NULL DEFAULT 'system',
    message TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_events_at ON events(at DESC);

CREATE TABLE IF NOT EXISTS service_metrics (
    time TIMESTAMPTZ NOT NULL,
    device_id UUID NOT NULL,
    service_id UUID NOT NULL,
    value DOUBLE PRECISION NOT NULL DEFAULT 0,
    rtt_ms DOUBLE PRECISION,
    packet_loss_pct REAL
);

SELECT create_hypertable('service_metrics', 'time', if_not_exists => TRUE, chunk_time_interval => INTERVAL '1 day');
CREATE INDEX IF NOT EXISTS idx_service_metrics_lookup ON service_metrics (service_id, time DESC);

CREATE TABLE IF NOT EXISTS link_metrics (
    time TIMESTAMPTZ NOT NULL,
    link_id UUID NOT NULL,
    device_id UUID,
    if_index INT,
    rx_bps BIGINT NOT NULL DEFAULT 0,
    tx_bps BIGINT NOT NULL DEFAULT 0,
    in_errors INT NOT NULL DEFAULT 0,
    out_errors INT NOT NULL DEFAULT 0,
    oper_status SMALLINT NOT NULL DEFAULT 1
);

SELECT create_hypertable('link_metrics', 'time', if_not_exists => TRUE, chunk_time_interval => INTERVAL '1 day');
CREATE INDEX IF NOT EXISTS idx_link_metrics_lookup ON link_metrics (link_id, time DESC);

ALTER TABLE service_metrics SET (timescaledb.compress, timescaledb.compress_segmentby = 'service_id', timescaledb.compress_orderby = 'time DESC');
SELECT add_compression_policy('service_metrics', INTERVAL '7 days', if_not_exists => TRUE);
SELECT add_retention_policy('service_metrics', INTERVAL '180 days', if_not_exists => TRUE);

ALTER TABLE link_metrics SET (timescaledb.compress, timescaledb.compress_segmentby = 'link_id', timescaledb.compress_orderby = 'time DESC');
SELECT add_compression_policy('link_metrics', INTERVAL '7 days', if_not_exists => TRUE);
SELECT add_retention_policy('link_metrics', INTERVAL '180 days', if_not_exists => TRUE);

-- ─── Fase 2 · Paridad operativa ─────────────────────────────────────────────

ALTER TABLE probes ADD COLUMN IF NOT EXISTS available_expression TEXT NOT NULL DEFAULT '';
ALTER TABLE probes DROP CONSTRAINT IF EXISTS probes_probe_type_check;
ALTER TABLE probes ADD CONSTRAINT probes_probe_type_check
  CHECK (probe_type IN ('icmp','snmp','tcp','http','dns','function','logic','udp','ssh','smtp','ftp'));

CREATE TABLE IF NOT EXISTS address_lists (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    color VARCHAR(20) NOT NULL DEFAULT '#0f766e',
    kind VARCHAR(10) NOT NULL DEFAULT 'allow' CHECK (kind IN ('allow','block')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS address_list_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    list_id UUID NOT NULL REFERENCES address_lists(id) ON DELETE CASCADE,
    value CIDR NOT NULL,
    label VARCHAR(160) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (list_id, value)
);

CREATE INDEX IF NOT EXISTS idx_address_list_entries_list ON address_list_entries(list_id);

CREATE TABLE IF NOT EXISTS networks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(160) NOT NULL,
    cidr CIDR,
    address_list_id UUID REFERENCES address_lists(id) ON DELETE SET NULL,
    map_id UUID REFERENCES maps(id) ON DELETE SET NULL,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    snmp_profile_id UUID REFERENCES snmp_profiles(id) ON DELETE SET NULL,
    probe_ids UUID[] NOT NULL DEFAULT '{}',
    interval_sec INT NOT NULL DEFAULT 3600,
    mode VARCHAR(10) NOT NULL DEFAULT 'fast' CHECK (mode IN ('fast','reliable')),
    name_preference VARCHAR(20) NOT NULL DEFAULT 'dns_snmp_ip',
    auto_create BOOLEAN NOT NULL DEFAULT FALSE,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_scan_at TIMESTAMPTZ,
    next_scan_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_status VARCHAR(20) NOT NULL DEFAULT 'pending',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_networks_due ON networks(is_enabled, next_scan_at);

CREATE TABLE IF NOT EXISTS network_scan_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    network_id UUID NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    status VARCHAR(20) NOT NULL DEFAULT 'running',
    mode VARCHAR(10) NOT NULL DEFAULT 'fast',
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    scanned_count INT NOT NULL DEFAULT 0,
    alive_count INT NOT NULL DEFAULT 0,
    created_count INT NOT NULL DEFAULT 0,
    results JSONB NOT NULL DEFAULT '[]',
    error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_network_scan_runs_lookup ON network_scan_runs(network_id, started_at DESC);

CREATE TABLE IF NOT EXISTS dashboard_panels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(160) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    widgets JSONB NOT NULL DEFAULT '[]',
    is_shared BOOLEAN NOT NULL DEFAULT FALSE,
    is_noc BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dashboard_panels_user ON dashboard_panels(user_id);

CREATE TABLE IF NOT EXISTS notification_sounds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    url VARCHAR(500) NOT NULL,
    created_by VARCHAR(120) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE maps ADD COLUMN IF NOT EXISTS poll_interval_sec INT;
ALTER TABLE maps ADD COLUMN IF NOT EXISTS poll_down_retries INT;
ALTER TABLE maps ADD COLUMN IF NOT EXISTS poll_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL;
ALTER TABLE maps ADD COLUMN IF NOT EXISTS poll_notifications_enabled BOOLEAN;

ALTER TABLE device_groups ADD COLUMN IF NOT EXISTS poll_interval_sec INT;
ALTER TABLE device_groups ADD COLUMN IF NOT EXISTS poll_down_retries INT;
ALTER TABLE device_groups ADD COLUMN IF NOT EXISTS poll_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL;
ALTER TABLE device_groups ADD COLUMN IF NOT EXISTS poll_notifications_enabled BOOLEAN;

ALTER TABLE devices ADD COLUMN IF NOT EXISTS poll_interval_sec INT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS poll_down_retries INT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS poll_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS poll_notifications_enabled BOOLEAN;

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS filter_address_list_ids UUID[] NOT NULL DEFAULT '{}';

ALTER TABLE maintenance_windows ALTER COLUMN scope_type TYPE VARCHAR(20);
ALTER TABLE maintenance_windows DROP CONSTRAINT IF EXISTS maintenance_windows_scope_type_check;
ALTER TABLE maintenance_windows ADD CONSTRAINT maintenance_windows_scope_type_check
  CHECK (scope_type IN ('device','map','tag','address_list'));

INSERT INTO probes (id, name, probe_type, description, config, warning_expression, error_expression, value_expression, unit, default_interval_sec, default_timeout_ms, default_down_retries, is_builtin)
VALUES
('b0000000-0000-4000-8000-000000000001', 'Ping', 'icmp', 'ICMP Echo: latencia, jitter y pérdida.', '{"count":3}', '', '', '', 'ms', 15, 1800, 3, TRUE),
('b0000000-0000-4000-8000-000000000002', 'HTTP', 'http', 'HTTP/HTTPS con validación de estado y caducidad TLS.', '{"scheme":"http","path":"/","expected_status_min":200,"expected_status_max":399,"warn_tls_days":14,"fail_tls_days":3}', '', '', '', 'ms', 60, 5000, 3, TRUE),
('b0000000-0000-4000-8000-000000000003', 'TCP Port', 'tcp', 'Comprobación de puerto TCP.', '{"port":8291}', '', '', '', 'ms', 60, 2500, 3, TRUE),
('b0000000-0000-4000-8000-000000000004', 'SNMP Uptime', 'snmp', 'sysUpTime como valor y etiqueta.', '{"oids":["1.3.6.1.2.1.1.3.0"]}', '', '', 'uptime_format(oid("1.3.6.1.2.1.1.3.0"))', '', 60, 2500, 2, TRUE),
('b0000000-0000-4000-8000-000000000005', 'SNMP CPU', 'snmp', 'CPU de RouterOS con umbrales.', '{"oids":["1.3.6.1.4.1.14988.1.1.3.3.0"]}', 'if(oid("1.3.6.1.4.1.14988.1.1.3.3.0") > 80, "CPU alta", "")', 'if(oid("1.3.6.1.4.1.14988.1.1.3.3.0") > 95, "CPU crítica", "")', 'oid("1.3.6.1.4.1.14988.1.1.3.3.0")', '%', 30, 2500, 2, TRUE),
('b0000000-0000-4000-8000-000000000006', 'SNMP Temperature', 'snmp', 'Temperatura de la placa.', '{"oids":["1.3.6.1.4.1.14988.1.1.3.10.0"]}', 'if(oid("1.3.6.1.4.1.14988.1.1.3.10.0") / 10 > 65, "Temperatura alta", "")', 'if(oid("1.3.6.1.4.1.14988.1.1.3.10.0") / 10 > 80, "Temperatura crítica", "")', 'round(oid("1.3.6.1.4.1.14988.1.1.3.10.0") / 10, 1)', '°C', 60, 2500, 2, TRUE),
('b0000000-0000-4000-8000-000000000007', 'UDP Port', 'udp', 'Puerto UDP con respuesta opcional.', '{"port":53,"send":"","expect_response":false}', '', '', '', 'ms', 60, 2500, 3, TRUE),
('b0000000-0000-4000-8000-000000000008', 'SSH', 'ssh', 'Banner SSH con regex opcional.', '{"port":22,"expect_regex":"^SSH-"}', '', '', '', 'ms', 60, 3000, 3, TRUE),
('b0000000-0000-4000-8000-000000000009', 'SMTP', 'smtp', 'Saludo SMTP.', '{"port":25,"expect_code":"220"}', '', '', '', 'ms', 60, 4000, 3, TRUE),
('b0000000-0000-4000-8000-000000000010', 'FTP', 'ftp', 'Saludo FTP.', '{"port":21,"expect_code":"220"}', '', '', '', 'ms', 60, 4000, 3, TRUE)
ON CONFLICT (id) DO NOTHING;

-- --- Fase 3 · Gestión y diferenciación --------------------------------------

-- RouterOS: credenciales cifradas, plantillas y despliegues con aprobación.
CREATE TABLE IF NOT EXISTS routeros_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL UNIQUE,
    username VARCHAR(120) NOT NULL,
    password TEXT NOT NULL DEFAULT '',
    port INT NOT NULL DEFAULT 8728,
    use_tls BOOLEAN NOT NULL DEFAULT FALSE,
    insecure_tls BOOLEAN NOT NULL DEFAULT FALSE,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE devices ADD COLUMN IF NOT EXISTS routeros_credential_id UUID REFERENCES routeros_credentials(id) ON DELETE SET NULL;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS routeros_api_port INT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS routeros_last_sync_at TIMESTAMPTZ;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS routeros_last_error TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_devices_routeros_cred ON devices(routeros_credential_id);

CREATE TABLE IF NOT EXISTS routeros_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(140) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    category VARCHAR(30) NOT NULL DEFAULT 'config',
    content TEXT NOT NULL,
    variables JSONB NOT NULL DEFAULT '[]',
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    created_by VARCHAR(120) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS routeros_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(160) NOT NULL,
    template_id UUID REFERENCES routeros_templates(id) ON DELETE SET NULL,
    template_name VARCHAR(140) NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    content_hash CHAR(64) NOT NULL DEFAULT '',
    variables JSONB NOT NULL DEFAULT '{}',
    target_device_ids UUID[] NOT NULL DEFAULT '{}',
    status VARCHAR(20) NOT NULL DEFAULT 'pending_approval'
      CHECK (status IN ('pending_approval','approved','running','completed','failed','cancelled')),
    requires_approval BOOLEAN NOT NULL DEFAULT TRUE,
    created_by VARCHAR(120) NOT NULL DEFAULT '',
    approved_by VARCHAR(120) NOT NULL DEFAULT '',
    approved_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    notes TEXT NOT NULL DEFAULT '',
    plan JSONB NOT NULL DEFAULT '[]',
    stats JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_routeros_jobs_status ON routeros_jobs(status, created_at DESC);

CREATE TABLE IF NOT EXISTS routeros_job_targets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES routeros_jobs(id) ON DELETE CASCADE,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    device_name VARCHAR(150) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'pending'
      CHECK (status IN ('pending','running','success','failed','skipped')),
    output TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    before_state JSONB NOT NULL DEFAULT '{}',
    after_state JSONB NOT NULL DEFAULT '{}',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (job_id, device_id)
);

CREATE INDEX IF NOT EXISTS idx_routeros_job_targets_job ON routeros_job_targets(job_id, status);

-- SNMP Traps decodificados con MIB y reglas propias.
CREATE TABLE IF NOT EXISTS mib_notifications (
    module VARCHAR(120) NOT NULL DEFAULT '',
    oid TEXT NOT NULL,
    name VARCHAR(160) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    objects TEXT[] NOT NULL DEFAULT '{}',
    imported_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (module, oid)
);

CREATE INDEX IF NOT EXISTS idx_mib_notifications_oid ON mib_notifications(oid text_pattern_ops);
CREATE INDEX IF NOT EXISTS idx_mib_notifications_name ON mib_notifications(name);

CREATE TABLE IF NOT EXISTS trap_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(140) NOT NULL,
    priority INT NOT NULL DEFAULT 100,
    match_oid TEXT NOT NULL DEFAULT '',
    match_name TEXT NOT NULL DEFAULT '',
    severity VARCHAR(20) NOT NULL DEFAULT 'warning' CHECK (severity IN ('info','ok','warning','critical')),
    event_type VARCHAR(50) NOT NULL DEFAULT 'snmp_trap',
    message_template TEXT NOT NULL DEFAULT '',
    filter_device_ids UUID[] NOT NULL DEFAULT '{}',
    filter_tags TEXT[] NOT NULL DEFAULT '{}',
    filter_map_ids UUID[] NOT NULL DEFAULT '{}',
    channel_ids UUID[] NOT NULL DEFAULT '{}',
    notify BOOLEAN NOT NULL DEFAULT TRUE,
    suppress BOOLEAN NOT NULL DEFAULT FALSE,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS snmp_traps (
    id BIGSERIAL PRIMARY KEY,
    at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source_ip INET,
    device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    map_id UUID REFERENCES maps(id) ON DELETE SET NULL,
    trap_oid TEXT NOT NULL DEFAULT '',
    trap_name TEXT NOT NULL DEFAULT '',
    severity VARCHAR(20) NOT NULL DEFAULT 'warning',
    event_type VARCHAR(50) NOT NULL DEFAULT 'snmp_trap',
    message TEXT NOT NULL DEFAULT '',
    version VARCHAR(10) NOT NULL DEFAULT '',
    variables JSONB NOT NULL DEFAULT '[]',
    matched_rule_id UUID REFERENCES trap_rules(id) ON DELETE SET NULL,
    event_id BIGINT,
    root_cause_device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    suppressed BOOLEAN NOT NULL DEFAULT FALSE,
    raw JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_snmp_traps_at ON snmp_traps(at DESC);
CREATE INDEX IF NOT EXISTS idx_snmp_traps_device ON snmp_traps(device_id, at DESC);
CREATE INDEX IF NOT EXISTS idx_snmp_traps_oid ON snmp_traps(trap_oid);

-- Correlación de causa raíz y supresión aguas abajo.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS root_cause_device_id UUID REFERENCES devices(id) ON DELETE SET NULL;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS correlation_checked_at TIMESTAMPTZ;
ALTER TABLE outages ADD COLUMN IF NOT EXISTS suppress_notifications BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE outages ADD COLUMN IF NOT EXISTS correlation_depth INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_devices_root_cause ON devices(root_cause_device_id);

-- Importación desde The Dude (SQLite/XML).
CREATE TABLE IF NOT EXISTS dude_imports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    filename VARCHAR(255) NOT NULL DEFAULT '',
    source_kind VARCHAR(10) NOT NULL DEFAULT 'sqlite' CHECK (source_kind IN ('sqlite','xml')),
    status VARCHAR(20) NOT NULL DEFAULT 'preview' CHECK (status IN ('preview','applied','failed')),
    device_count INT NOT NULL DEFAULT 0,
    map_count INT NOT NULL DEFAULT 0,
    link_count INT NOT NULL DEFAULT 0,
    payload JSONB NOT NULL DEFAULT '{}',
    result JSONB NOT NULL DEFAULT '{}',
    warnings JSONB NOT NULL DEFAULT '[]',
    created_by VARCHAR(120) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    applied_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_dude_imports_created ON dude_imports(created_at DESC);

-- Informes avanzados: SLA por cliente, programación PDF y exportación externa.
CREATE TABLE IF NOT EXISTS sla_clients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(160) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    device_ids UUID[] NOT NULL DEFAULT '{}',
    map_ids UUID[] NOT NULL DEFAULT '{}',
    tags TEXT[] NOT NULL DEFAULT '{}',
    target_pct NUMERIC(5,2) NOT NULL DEFAULT 99.50,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS report_schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(160) NOT NULL,
    report_type VARCHAR(30) NOT NULL DEFAULT 'sla'
      CHECK (report_type IN ('sla','interfaces','executive','clients')),
    format VARCHAR(10) NOT NULL DEFAULT 'pdf' CHECK (format IN ('pdf','csv')),
    frequency VARCHAR(10) NOT NULL DEFAULT 'weekly' CHECK (frequency IN ('daily','weekly','monthly')),
    hour_utc INT NOT NULL DEFAULT 6,
    day_of_week INT NOT NULL DEFAULT 1,
    day_of_month INT NOT NULL DEFAULT 1,
    range_days INT NOT NULL DEFAULT 30,
    map_id UUID REFERENCES maps(id) ON DELETE SET NULL,
    client_id UUID REFERENCES sla_clients(id) ON DELETE SET NULL,
    include_interfaces BOOLEAN NOT NULL DEFAULT TRUE,
    export_external BOOLEAN NOT NULL DEFAULT FALSE,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(120) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_report_schedules_due ON report_schedules(is_enabled, next_run_at);

CREATE TABLE IF NOT EXISTS report_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id UUID REFERENCES report_schedules(id) ON DELETE SET NULL,
    report_type VARCHAR(30) NOT NULL DEFAULT 'sla',
    format VARCHAR(10) NOT NULL DEFAULT 'pdf',
    status VARCHAR(20) NOT NULL DEFAULT 'running' CHECK (status IN ('running','completed','failed')),
    period_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    period_to TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    filename VARCHAR(255) NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    external_url TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    generated_by VARCHAR(120) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_report_runs_created ON report_runs(created_at DESC);
