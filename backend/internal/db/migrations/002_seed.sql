-- Demo topology. Fixed UUIDs make this seed idempotent.
INSERT INTO snmp_profiles (id, name, version, community, is_default)
VALUES ('90000000-0000-4000-8000-000000000001', 'Default v2c', 'v2c', 'public', TRUE)
ON CONFLICT (id) DO NOTHING;

INSERT INTO probes (id, name, probe_type, description, config, warning_expression, error_expression, value_expression, unit, default_interval_sec, default_timeout_ms, default_down_retries, is_builtin)
VALUES
('b0000000-0000-4000-8000-000000000001', 'Ping', 'icmp', 'ICMP Echo (3 packets): latency, jitter and packet loss.', '{"count":3}', '', '', '', 'ms', 15, 1800, 3, TRUE),
('b0000000-0000-4000-8000-000000000002', 'HTTP', 'http', 'HTTP/HTTPS GET with status validation and TLS expiry warning.', '{"scheme":"http","path":"/","expected_status_min":200,"expected_status_max":399,"warn_tls_days":14,"fail_tls_days":3}', '', '', '', 'ms', 60, 5000, 3, TRUE),
('b0000000-0000-4000-8000-000000000003', 'TCP Port', 'tcp', 'TCP connect check with optional banner regex.', '{"port":8291}', '', '', '', 'ms', 60, 2500, 3, TRUE),
('b0000000-0000-4000-8000-000000000004', 'SNMP Uptime', 'snmp', 'Read sysUpTime and show it as a dynamic label.', '{"oids":["1.3.6.1.2.1.1.3.0"]}', '', '', 'uptime_format(oid("1.3.6.1.2.1.1.3.0"))', '', 60, 2500, 2, TRUE),
('b0000000-0000-4000-8000-000000000005', 'SNMP CPU', 'snmp', 'MikroTik RouterOS CPU usage with warning and critical thresholds.', '{"oids":["1.3.6.1.4.1.14988.1.1.3.3.0"]}', 'if(oid("1.3.6.1.4.1.14988.1.1.3.3.0") > 80, "CPU alta", "")', 'if(oid("1.3.6.1.4.1.14988.1.1.3.3.0") > 95, "CPU crítica", "")', 'oid("1.3.6.1.4.1.14988.1.1.3.3.0")', '%', 30, 2500, 2, TRUE),
('b0000000-0000-4000-8000-000000000006', 'SNMP Temperature', 'snmp', 'MikroTik board temperature.', '{"oids":["1.3.6.1.4.1.14988.1.1.3.10.0"]}', 'if(oid("1.3.6.1.4.1.14988.1.1.3.10.0") / 10 > 65, "Temperatura alta", "")', 'if(oid("1.3.6.1.4.1.14988.1.1.3.10.0") / 10 > 80, "Temperatura crítica", "")', 'round(oid("1.3.6.1.4.1.14988.1.1.3.10.0") / 10, 1)', '°C', 60, 2500, 2, TRUE)
ON CONFLICT (id) DO NOTHING;

INSERT INTO probes (id, name, probe_type, description, config, warning_expression, error_expression, value_expression, unit, default_interval_sec, default_timeout_ms, default_down_retries, is_builtin)
VALUES
('b0000000-0000-4000-8000-000000000007', 'UDP Port', 'udp', 'UDP port check; optionally waits for a response.', '{"port":53,"send":"","expect_response":false}', '', '', '', 'ms', 60, 2500, 3, TRUE),
('b0000000-0000-4000-8000-000000000008', 'SSH', 'ssh', 'SSH banner check with optional regex.', '{"port":22,"expect_regex":"^SSH-"}', '', '', '', 'ms', 60, 3000, 3, TRUE),
('b0000000-0000-4000-8000-000000000009', 'SMTP', 'smtp', 'SMTP greeting check.', '{"port":25,"expect_code":"220"}', '', '', '', 'ms', 60, 4000, 3, TRUE),
('b0000000-0000-4000-8000-000000000010', 'FTP', 'ftp', 'FTP greeting check.', '{"port":21,"expect_code":"220"}', '', '', '', 'ms', 60, 4000, 3, TRUE)
ON CONFLICT (id) DO NOTHING;

INSERT INTO functions (id, name, description, expression_code, unit, is_builtin)
VALUES
('f0000000-0000-4000-8000-000000000001', 'cpu_usage', 'CPU usage (MikroTik RouterOS).', 'oid("1.3.6.1.4.1.14988.1.1.3.3.0")', '%', TRUE),
('f0000000-0000-4000-8000-000000000002', 'board_temp', 'Board temperature (MikroTik RouterOS).', 'round(oid("1.3.6.1.4.1.14988.1.1.3.10.0") / 10, 1)', '°C', TRUE),
('f0000000-0000-4000-8000-000000000003', 'sys_uptime', 'System uptime rendered as 12d 4h 08m.', 'uptime_format(oid("1.3.6.1.2.1.1.3.0"))', '', TRUE),
('f0000000-0000-4000-8000-000000000004', 'poe_power', 'PoE consumption (MikroTik RouterOS).', 'round(oid("1.3.6.1.4.1.14988.1.1.3.12.0") / 10, 1)', 'W', TRUE)
ON CONFLICT (id) DO NOTHING;

INSERT INTO notification_channels (id, name, channel_type, config, title_template, body_template)
VALUES ('c0000000-0000-4000-8000-000000000001', 'NOC Browser', 'web_audio', '{"sound":true,"tts":true}', '[{{Status}}] {{Device.Name}}', '{{Device.Name}} ({{Device.FirstAddress}}) - {{Event.Message}}')
ON CONFLICT (id) DO NOTHING;

INSERT INTO alert_rules (id, name, priority, trigger_transitions, delay_sec, channel_ids)
VALUES ('d0000000-0000-4000-8000-000000000001', 'Default NOC alerts', 100, '{down,warning,recovered}', 0, ARRAY['c0000000-0000-4000-8000-000000000001'::uuid])
ON CONFLICT (id) DO NOTHING;

INSERT INTO maps (id, parent_map_id, name, slug, description, default_device_shape, default_device_label)
VALUES
('10000000-0000-4000-8000-000000000001', NULL, 'Red General', 'red-general', 'Vista global de la infraestructura monitorizada.', 'rounded_rect', '[Device.Name]\n[Device.FirstAddress]'),
('10000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000001', 'Sede Central', 'sede-central', 'CPD principal, servidores y servicios críticos.', 'rounded_rect', '[Device.Name]\n[Device.FirstAddress]'),
('10000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000001', 'Torres WISP', 'torres-wisp', 'Radioenlaces y puntos de acceso de distribución.', 'circle', '[Device.Name]\n[Device.FirstAddress]')
ON CONFLICT (id) DO NOTHING;

INSERT INTO devices (id, name, primary_ip, dns_name, device_type, snmp_profile_id, status, status_reason, is_monitoring_enabled, tags, custom_fields, notes, last_seen_up_at, last_status_change_at)
VALUES
('a0000000-0000-4000-8000-000000000001', 'CCR2004-Core', '1.1.1.1', 'core.netmonitor.local', 'router', '90000000-0000-4000-8000-000000000001', 'up', 'Demo device', TRUE, '{core,critical}', '{"site":"CPD Central","rack":"R1"}', 'Router de borde y núcleo MPLS.', NOW(), NOW()),
('a0000000-0000-4000-8000-000000000002', 'CRS326-Distribucion', '8.8.8.8', 'sw-core.netmonitor.local', 'switch', '90000000-0000-4000-8000-000000000001', 'up', 'Demo device', TRUE, '{core,switching}', '{"site":"CPD Central","rack":"R1"}', 'Switch de distribución principal.', NOW(), NOW()),
('a0000000-0000-4000-8000-000000000003', 'Firewall-Borde', '9.9.9.9', 'fw.netmonitor.local', 'firewall', '90000000-0000-4000-8000-000000000001', 'up', 'Demo device', TRUE, '{core,security}', '{"site":"CPD Central"}', 'Salida a Internet y VPN.', NOW(), NOW()),
('a0000000-0000-4000-8000-000000000004', 'AP-Sectorial-Norte', '208.67.222.222', 'ap-norte.netmonitor.local', 'access_point', NULL, 'warning', 'Latencia elevada en el enlace radio', TRUE, '{wisp,radio}', '{"site":"Torre Norte","clients":"24"}', 'AP sectorial 5 GHz.', NOW(), NOW()),
('a0000000-0000-4000-8000-000000000005', 'SRV-NOC-01', '1.0.0.1', 'noc01.netmonitor.local', 'server', NULL, 'up', 'Demo device', TRUE, '{server,critical}', '{"site":"CPD Central","rack":"R4"}', 'Servidor de monitorización y utilidades.', NOW(), NOW()),
('a0000000-0000-4000-8000-000000000006', 'IMP-Rack-Contabilidad', '10.255.255.1', 'printer.netmonitor.local', 'printer', NULL, 'down', 'Ping timeout (3/3)', TRUE, '{office}', '{"site":"Sede Central","toner":"38"}', 'Impresora láser departamento contable.', NOW() - INTERVAL '2 days', NOW() - INTERVAL '9 minutes'),
('a0000000-0000-4000-8000-000000000007', 'SAI-CPD', '10.255.255.2', 'ups.netmonitor.local', 'ups', NULL, 'disabled', 'Mantenimiento programado', FALSE, '{power,critical}', '{"site":"CPD Central","battery":"97"}', 'SAI principal con tarjeta SNMP.', NOW(), NOW()),
('a0000000-0000-4000-8000-000000000008', 'RB5009-Torre-Norte', '8.8.4.4', 'rb-norte.netmonitor.local', 'router', '90000000-0000-4000-8000-000000000001', 'up', 'Demo device', TRUE, '{wisp,radio}', '{"site":"Torre Norte"}', 'Router de torre y alimentación PoE.', NOW(), NOW()),
('a0000000-0000-4000-8000-000000000009', 'CPE-Torre-Norte-01', '10.255.255.3', 'cpe01.netmonitor.local', 'cpe', NULL, 'unreachable', 'Depende de RB5009-Torre-Norte', TRUE, '{wisp,cpe}', '{"site":"Torre Norte","signal":"-61"}', 'Cliente residencial enlace 60 GHz.', NOW() - INTERVAL '3 days', NOW() - INTERVAL '40 minutes'),
('a0000000-0000-4000-8000-00000000000a', 'CPE-Torre-Norte-02', '10.255.255.4', 'cpe02.netmonitor.local', 'cpe', NULL, 'down', 'Ping timeout (3/3)', TRUE, '{wisp,cpe}', '{"site":"Torre Norte","signal":"-74"}', 'Cliente PYME con enlace redundante.', NOW() - INTERVAL '1 day', NOW() - INTERVAL '1 hour')
ON CONFLICT (id) DO NOTHING;

INSERT INTO device_parents (device_id, parent_device_id)
VALUES
('a0000000-0000-4000-8000-000000000009', 'a0000000-0000-4000-8000-000000000008'),
('a0000000-0000-4000-8000-00000000000a', 'a0000000-0000-4000-8000-000000000008')
ON CONFLICT DO NOTHING;

INSERT INTO map_elements (id, map_id, element_type, device_id, target_submap_id, pos_x, pos_y, width, height, shape, icon_name, label_template, z_index, custom_style)
VALUES
('e1000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'device', 'a0000000-0000-4000-8000-000000000003', NULL, 430, 70, 210, 96, 'pill', 'shield', '[Device.Name]\n[Device.FirstAddress] · Borde', 10, '{}'),
('e1000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000001', 'device', 'a0000000-0000-4000-8000-000000000001', NULL, 430, 240, 220, 108, 'rounded_rect', 'router', '[Device.Name]\n[Device.FirstAddress] · [Device.RTT] ms\n[Device.StatusText]', 10, '{}'),
('e1000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000001', 'device', 'a0000000-0000-4000-8000-000000000002', NULL, 430, 430, 220, 108, 'rounded_rect', 'network', '[Device.Name]\n[Device.FirstAddress] · [Device.RTT] ms', 10, '{}'),
('e1000000-0000-4000-8000-000000000004', '10000000-0000-4000-8000-000000000001', 'submap', NULL, '10000000-0000-4000-8000-000000000002', 760, 415, 230, 130, 'rounded_rect', 'building', 'Sede Central\n[Submap.StatusText]', 10, '{}'),
('e1000000-0000-4000-8000-000000000005', '10000000-0000-4000-8000-000000000001', 'submap', NULL, '10000000-0000-4000-8000-000000000003', 110, 415, 230, 130, 'rounded_rect', 'radio-tower', 'Torres WISP\n[Submap.StatusText]', 10, '{}'),
('e1000000-0000-4000-8000-000000000006', '10000000-0000-4000-8000-000000000001', 'device', 'a0000000-0000-4000-8000-000000000004', NULL, 790, 210, 168, 168, 'circle', 'wifi', '[Device.Name]\n[Device.FirstAddress]', 10, '{}'),
('e1000000-0000-4000-8000-000000000007', '10000000-0000-4000-8000-000000000001', 'device', 'a0000000-0000-4000-8000-000000000005', NULL, 100, 210, 200, 100, 'rounded_rect', 'server', '[Device.Name]\n[Device.FirstAddress]', 10, '{}'),

('e2000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000002', 'device', 'a0000000-0000-4000-8000-000000000002', NULL, 400, 120, 220, 108, 'rounded_rect', 'network', '[Device.Name]\n[Device.FirstAddress]', 10, '{}'),
('e2000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000002', 'device', 'a0000000-0000-4000-8000-000000000005', NULL, 120, 340, 200, 100, 'rounded_rect', 'server', 'SRV-NOC-01\n[Device.FirstAddress]\nCPU [f:sys_uptime()]', 10, '{}'),
('e2000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000002', 'device', 'a0000000-0000-4000-8000-000000000006', NULL, 400, 340, 200, 100, 'square', 'printer', 'IMP-Rack-Contabilidad\n[Device.FirstAddress]', 10, '{}'),
('e2000000-0000-4000-8000-000000000004', '10000000-0000-4000-8000-000000000002', 'device', 'a0000000-0000-4000-8000-000000000007', NULL, 680, 340, 180, 100, 'hexagon', 'battery-charging', 'SAI-CPD\nBatería [Device.Custom.battery]%', 10, '{}'),
('e2000000-0000-4000-8000-000000000005', '10000000-0000-4000-8000-000000000002', 'device', 'a0000000-0000-4000-8000-000000000004', NULL, 680, 120, 150, 150, 'circle', 'wifi', 'AP-Norte\n[Device.FirstAddress]', 10, '{}'),

('e3000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000003', 'device', 'a0000000-0000-4000-8000-000000000008', NULL, 390, 120, 230, 110, 'rounded_rect', 'router', 'RB5009-Torre-Norte\n[Device.FirstAddress]', 10, '{}'),
('e3000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000003', 'device', 'a0000000-0000-4000-8000-000000000009', NULL, 140, 330, 160, 160, 'circle', 'wifi', 'CPE-Norte-01\n[Device.FirstAddress]\n[Device.Custom.signal] dBm', 10, '{}'),
('e3000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000003', 'device', 'a0000000-0000-4000-8000-00000000000a', NULL, 640, 330, 160, 160, 'circle', 'wifi', 'CPE-Norte-02\n[Device.FirstAddress]\n[Device.Custom.signal] dBm', 10, '{}')
ON CONFLICT (id) DO NOTHING;

INSERT INTO map_links (id, map_id, source_element_id, target_element_id, mastering_type, link_type, max_speed_bps, thickness, dashed)
VALUES
('d1000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'e1000000-0000-4000-8000-000000000001', 'e1000000-0000-4000-8000-000000000002', 'simple', 'fiber', 10000000000, 4, FALSE),
('d1000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000001', 'e1000000-0000-4000-8000-000000000002', 'e1000000-0000-4000-8000-000000000003', 'simple', 'fiber', 10000000000, 4, FALSE),
('d1000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000001', 'e1000000-0000-4000-8000-000000000002', 'e1000000-0000-4000-8000-000000000004', 'simple', 'ethernet', 1000000000, 2.5, FALSE),
('d1000000-0000-4000-8000-000000000004', '10000000-0000-4000-8000-000000000001', 'e1000000-0000-4000-8000-000000000002', 'e1000000-0000-4000-8000-000000000005', 'simple', 'wireless', 1000000000, 2, TRUE),
('d1000000-0000-4000-8000-000000000005', '10000000-0000-4000-8000-000000000001', 'e1000000-0000-4000-8000-000000000002', 'e1000000-0000-4000-8000-000000000006', 'simple', 'wireless', 1000000000, 2, TRUE),
('d1000000-0000-4000-8000-000000000006', '10000000-0000-4000-8000-000000000001', 'e1000000-0000-4000-8000-000000000002', 'e1000000-0000-4000-8000-000000000007', 'simple', 'ethernet', 1000000000, 2.5, FALSE),
('d2000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000002', 'e2000000-0000-4000-8000-000000000001', 'e2000000-0000-4000-8000-000000000002', 'simple', 'ethernet', 1000000000, 2.5, FALSE),
('d2000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000002', 'e2000000-0000-4000-8000-000000000001', 'e2000000-0000-4000-8000-000000000003', 'simple', 'ethernet', 1000000000, 2.5, FALSE),
('d2000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000002', 'e2000000-0000-4000-8000-000000000001', 'e2000000-0000-4000-8000-000000000004', 'simple', 'ethernet', 1000000000, 2.5, FALSE),
('d2000000-0000-4000-8000-000000000004', '10000000-0000-4000-8000-000000000002', 'e2000000-0000-4000-8000-000000000001', 'e2000000-0000-4000-8000-000000000005', 'simple', 'wireless', 1000000000, 2, TRUE),
('d3000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000003', 'e3000000-0000-4000-8000-000000000001', 'e3000000-0000-4000-8000-000000000002', 'simple', 'wireless', 1000000000, 2, TRUE),
('d3000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000003', 'e3000000-0000-4000-8000-000000000001', 'e3000000-0000-4000-8000-000000000003', 'simple', 'wireless', 1000000000, 2, TRUE)
ON CONFLICT (id) DO NOTHING;

INSERT INTO device_services (id, device_id, probe_id, custom_name, interval_sec, status, last_output_message)
VALUES
('51000000-0000-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000001', 'Ping', 15, 'unknown', ''),
('51000000-0000-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000002', 'b0000000-0000-4000-8000-000000000001', 'Ping', 15, 'unknown', ''),
('51000000-0000-4000-8000-000000000003', 'a0000000-0000-4000-8000-000000000003', 'b0000000-0000-4000-8000-000000000001', 'Ping', 15, 'unknown', ''),
('51000000-0000-4000-8000-000000000004', 'a0000000-0000-4000-8000-000000000004', 'b0000000-0000-4000-8000-000000000001', 'Ping', 15, 'unknown', ''),
('51000000-0000-4000-8000-000000000005', 'a0000000-0000-4000-8000-000000000005', 'b0000000-0000-4000-8000-000000000001', 'Ping', 15, 'unknown', ''),
('51000000-0000-4000-8000-000000000006', 'a0000000-0000-4000-8000-000000000006', 'b0000000-0000-4000-8000-000000000001', 'Ping', 15, 'unknown', ''),
('51000000-0000-4000-8000-000000000007', 'a0000000-0000-4000-8000-000000000008', 'b0000000-0000-4000-8000-000000000001', 'Ping', 15, 'unknown', ''),
('51000000-0000-4000-8000-000000000008', 'a0000000-0000-4000-8000-000000000009', 'b0000000-0000-4000-8000-000000000001', 'Ping', 15, 'unknown', ''),
('51000000-0000-4000-8000-000000000009', 'a0000000-0000-4000-8000-00000000000a', 'b0000000-0000-4000-8000-000000000001', 'Ping', 15, 'unknown', ''),
('51000000-0000-4000-8000-00000000000a', 'a0000000-0000-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000005', 'CPU', 30, 'unknown', ''),
('51000000-0000-4000-8000-00000000000b', 'a0000000-0000-4000-8000-000000000008', 'b0000000-0000-4000-8000-000000000006', 'Temperatura', 60, 'unknown', '')
ON CONFLICT (id) DO NOTHING;

-- SNMP threshold probes are advisory in the demo: a failed CPU/temperature
-- reading turns the device amber but does not mark the whole router DOWN.
UPDATE device_services SET is_critical=FALSE
WHERE id IN ('51000000-0000-4000-8000-00000000000a', '51000000-0000-4000-8000-00000000000b');

INSERT INTO outages (id, device_id, device_service_id, severity, started_at, error_message, acknowledged)
VALUES ('71000000-0000-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000006', '51000000-0000-4000-8000-000000000006', 'down', NOW() - INTERVAL '9 minutes', 'Ping timeout (3/3) - 100% packet loss', FALSE)
ON CONFLICT (id) DO NOTHING;

INSERT INTO events (at, device_id, map_id, severity, event_type, message)
SELECT NOW() - (n || ' minutes')::interval,
       'a0000000-0000-4000-8000-000000000006',
       '10000000-0000-4000-8000-000000000002',
       'critical', 'outage', 'IMP-Rack-Contabilidad dejó de responder a ICMP'
FROM generate_series(1, 1) n
WHERE NOT EXISTS (SELECT 1 FROM events WHERE event_type = 'outage' AND device_id = 'a0000000-0000-4000-8000-000000000006');

-- Synthetic traffic for the demo links so the white canvas has live-looking
-- link pills and 24h charts before the first SNMP link is configured.
INSERT INTO link_metrics (time, link_id, device_id, if_index, rx_bps, tx_bps, in_errors, out_errors, oper_status)
SELECT NOW() - (step * INTERVAL '2 minutes'),
       l.id,
       l.master_device_id,
       COALESCE(l.snmp_if_index, 1),
       (ABS(hashtext(l.id::text)) % 500000000) + 18000000 + (step % 37) * 900000,
       (ABS(hashtext(l.id::text)) % 300000000) + 9000000 + (step % 23) * 600000,
       0,
       0,
       1
FROM map_links l
CROSS JOIN generate_series(1, 180) AS step
WHERE NOT EXISTS (SELECT 1 FROM link_metrics);
