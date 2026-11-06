export type Status = 'up' | 'warning' | 'down' | 'unreachable' | 'acknowledged' | 'disabled' | 'unknown'
export type Shape = 'circle' | 'square' | 'rounded_rect' | 'pill' | 'diamond' | 'hexagon'

export interface Counts {
  up: number
  warning: number
  down: number
  acknowledged: number
  unreachable: number
  disabled: number
  unknown: number
}

export interface MapInfo {
  id: string
  parent_map_id: string
  name: string
  slug: string
  description: string
  grid_enabled: boolean
  grid_size: number
  snap_to_grid: boolean
  background_color: string
  status: Status
  status_text: string
  counts: Counts
}

export interface MapMeta {
  id: string
  name: string
  parent_map_id: string
  description: string
  grid_enabled: boolean
  grid_size: number
  snap_to_grid: boolean
  background_color: string
  default_device_label: string
  default_device_shape: string
  background_image: string
  background_opacity: number
  poll_interval_sec: number
  poll_down_retries: number
  poll_agent_id: string
  poll_notifications: number
  viewport: { x: number; y: number; zoom: number } | null
}

export interface DeviceBrief {
  id: string
  name: string
  primary_ip: string
  secondary_ips?: string[]
  status: Status
  status_reason: string
  is_acknowledged: boolean
  tags: string[]
  custom_fields: Record<string, unknown>
  rtt_ms: number
  packet_loss_pct: number
  root_cause_device_id?: string
  root_cause?: string
  services_summary: { up: number; warning: number; down: number }
}

export interface SubmapBrief {
  id: string
  name: string
  status: Status
  counts: Counts
  status_text: string
}

export interface NetworkBrief {
  cidr: string
  status: Status
  counts: Counts
  status_text: string
}

export interface CanvasElement {
  id: string
  element_type: 'device' | 'submap' | 'network' | 'zone' | 'text_label'
  device_id: string
  target_submap_id: string
  network_cidr: string
  pos_x: number
  pos_y: number
  width: number
  height: number
  rotation?: number
  zone_id?: string
  shape: Shape
  icon_name: string
  show_icon: boolean
  label_template: string
  label_position: string
  z_index: number
  is_locked: boolean
  custom_style?: Record<string, unknown>
  status: Status
  status_text: string
  rendered_label: string
  device: DeviceBrief | null
  submap: SubmapBrief | null
  network: NetworkBrief | null
}

export interface CanvasLink {
  id: string
  map_id?: string
  source_element_id: string
  target_element_id: string
  source_handle: string
  target_handle: string
  mastering_type: 'simple' | 'snmp'
  master_device_id: string
  snmp_if_index: number
  snmp_if_name: string
  link_type: string
  max_speed_bps: number
  warning_utilization_pct: number
  critical_utilization_pct: number
  color_by_traffic: boolean
  routing_style: 'straight' | 'orthogonal' | 'bezier'
  thickness: number
  dashed: boolean
  label_template: string
  status: string
  rx_bps: number
  tx_bps: number
  utilization_pct: number
  utilization_level: 'normal' | 'warning' | 'critical' | 'down'
  rendered_label: string
}

export interface SnmpSystemInfo {
  name: string
  descr: string
  object_id: string
  contact: string
  location: string
  uptime: string
  uptime_seconds: number
  services: number
}

export interface SnmpIPInfo {
  address: string
  mask: string
  if_index: number
  if_name: string
  bcast: number
}

export interface SnmpInterfaceInfo {
  if_index: number
  name: string
  descr: string
  alias: string
  type: number
  mtu: number
  mac: string
  admin_status: number
  oper_status: number
  speed_mbps: number
  in_octets: number
  out_octets: number
  in_errors: number
  out_errors: number
  in_discards: number
  out_discards: number
}

export interface SnmpARPInfo {
  ip: string
  mac: string
  if_index: number
  if_name: string
}

export interface SnmpRouteInfo {
  destination: string
  mask: string
  next_hop: string
  if_index: number
  if_name: string
  type: number
  metric: number
}

export interface SnmpDeviceInfo {
  system: SnmpSystemInfo
  ips: SnmpIPInfo[]
  interfaces: SnmpInterfaceInfo[]
  arp: SnmpARPInfo[]
  routes: SnmpRouteInfo[]
  warnings: string[]
}

export interface CanvasData {
  map: MapMeta
  elements: CanvasElement[]
  links: CanvasLink[]
  summary: Counts & { status: Status }
  breadcrumbs: { id: string; name: string }[]
}

export interface DeviceService {
  id: string
  probe_id: string
  probe_name: string
  probe_type: string
  name: string
  status: Status
  status_text: string
  interval_sec: number
  timeout_ms: number
  down_retries: number
  is_critical: boolean
  is_enabled: boolean
  last_value: number
  last_output_message: string
  unit: string
  last_check_at: string | null
}

export interface Device {
  id: string
  name: string
  primary_ip: string
  secondary_ips?: string[]
  dns_name: string
  mac_address: string
  vendor: string
  device_type: string
  status: Status
  status_text: string
  status_reason: string
  is_acknowledged: boolean
  tags: string[]
  custom_fields: Record<string, unknown>
  snmp_profile_id: string
  agent_id?: string
  notes: string
  is_monitoring_enabled: boolean
  snmp_refresh?: {
    system_sec: number
    interfaces_sec: number
    ips_sec: number
    arp_sec: number
    routes_sec: number
    overrides: Partial<Record<'system_sec' | 'interfaces_sec' | 'ips_sec' | 'arp_sec' | 'routes_sec', number>>
  }
  last_status_change_at: string
  last_seen_up_at: string
  parent_ids: string[]
  poll_interval_sec?: number
  poll_down_retries?: number
  poll_agent_id?: string
  poll_notifications?: number
  services: DeviceService[]
  maintenance_until?: string
  maintenance_note?: string
  services_summary?: { up: number; warning: number; down: number }
}

export interface Outage {
  id: string
  device_id: string
  device_name: string
  primary_ip: string
  severity: string
  started_at: string
  ended_at: string | null
  duration_sec: number
  error_message: string
  acknowledged: boolean
  acknowledged_by: string
  map_name: string
  map_id: string
  root_cause_device_id?: string
  root_cause?: string
  suppress_notifications?: boolean
  correlation_depth?: number
  active?: boolean
}

export interface EventItem {
  id: number
  at: string
  device_id: string
  device_name: string
  severity: string
  event_type: string
  message: string
  map_name: string
}

export interface Probe {
  id: string
  name: string
  probe_type: string
  description: string
  config: Record<string, unknown>
  available_expression: string
  warning_expression: string
  error_expression: string
  value_expression: string
  unit: string
  default_interval_sec: number
  default_timeout_ms: number
  default_down_retries: number
  is_builtin: boolean
}

export interface FunctionDef {
  id: string
  name: string
  description: string
  expression_code: string
  unit: string
  is_builtin: boolean
}

export interface SnmpProfile {
  id: string
  name: string
  version: string
  port: number
  timeout_ms: number
  retries: number
  community: string
  is_default: boolean
  v3_username: string
  v3_security_level: string
}

export interface Agent {
  id: string
  name: string
  version: string
  last_seen_at: string | null
  max_devices: number
  is_enabled: boolean
  created_at: string
  device_count: number
  online: boolean
}

export interface NotificationChannel {
  id: string
  name: string
  channel_type: string
  config: Record<string, unknown>
  is_enabled: boolean
}

export interface DashboardSummary {
  devices: { total: number; up: number; warning: number; down: number; unreachable: number; disabled: number; unknown: number }
  maps: number
  probes: number
  active_outages: number
  active_outages_acknowledged: number
  active_outages_unacknowledged: number
  server_time: string
}

export interface AlertEvent {
  transition: string
  severity: string
  device_id: string
  device_name: string
  ip: string
  map_id: string
  map_name: string
  message: string
  acknowledged: boolean
  timestamp: string
}

export interface AddressListEntry {
  id: string
  value: string
  cidr?: string
  label: string
}

export interface AddressList {
  id: string
  name: string
  description: string
  color: string
  kind: 'allow' | 'block'
  entry_count: number
  created_at?: string
  entries?: AddressListEntry[]
}

export interface DiscoveryHost {
  ip: string
  alive: boolean
  name: string
  name_source: string
  sys_descr: string
  sys_object_id: string
  latency_ms: number
  existing_device_id: string
}

export interface DiscoveryRun {
  id: string
  network_id: string
  started_at: string
  finished_at?: string
  status: string
  mode: string
  agent_id?: string
  scanned_count: number
  alive_count: number
  created_count: number
  results?: DiscoveryHost[]
  error?: string
}

export interface DiscoveryNetwork {
  id: string
  name: string
  cidr: string
  address_list_id: string
  map_id: string
  agent_id: string
  snmp_profile_id: string
  probe_ids: string[]
  interval_sec: number
  mode: 'fast' | 'reliable'
  name_preference: string
  auto_create: boolean
  is_enabled: boolean
  last_scan_at?: string
  next_scan_at?: string
  last_status: string
  last_error: string
  map_name: string
  agent_name: string
  address_list_name: string
  snmp_profile_name: string
  last_scanned: number
  last_alive: number
  last_created: number
}

export interface MetricPoint {
  time: string
  value: number
}

export interface MetricSeries {
  id: string
  kind: string
  label: string
  unit: string
  points: MetricPoint[]
}

export interface PanelWidget {
  id: string
  kind: 'chart' | 'table' | 'map' | 'counter' | 'text'
  title: string
  series?: string[]
  range?: string
  metric?: string
  source?: string
  limit?: number
  map_id?: string
  text?: string
}

export interface DashboardPanel {
  id: string
  user_id: string
  owner: string
  name: string
  description: string
  widgets: PanelWidget[]
  is_shared: boolean
  is_noc: boolean
  created_at: string
  updated_at: string
}

export interface NotificationSound {
  id: string
  name: string
  filename: string
  url: string
  created_by: string
  created_at: string
}

export interface NOCSettings {
  sound_url: string
  popup: boolean
  flash: boolean
}

// --- Fase 3 - Gestion y diferenciacion -------------------------------

export type DeviceCredentialKind = 'winbox' | 'web' | 'ssh' | 'api' | 'other'

export interface DeviceToolAccess {
  kind: DeviceCredentialKind
  source: 'device' | 'routeros' | 'none'
  username: string
  port: number
  use_tls: boolean
  insecure_tls: boolean
  path: string
  notes: string
  has_password: boolean
}

export interface RouterOSCredential {
  id: string
  name: string
  username: string
  port: number
  use_tls: boolean
  insecure_tls: boolean
  description: string
  has_password: boolean
  created_at?: string
  updated_at?: string
}

export interface RouterOSDevice {
  id: string
  name: string
  primary_ip: string
  device_type: string
  status: Status
  routeros_version: string
  hardware_model: string
  sys_name: string
  vendor: string
  credential_id: string
  credential_name: string
  api_port: number
  last_sync_at: string | null
  last_error: string
  is_managed: boolean
}

export interface RouterOSInventory {
  devices: RouterOSDevice[]
  managed: number
  total: number
}

export interface RouterOSTemplate {
  id: string
  name: string
  description: string
  category: string
  content: string
  variables: unknown
  is_builtin: boolean
  created_by: string
  updated_at: string
}

export interface RouterOSPlanItem {
  device_id: string
  device_name: string
  primary_ip: string
  credential: string
  content: string
  content_hash: string
}

export interface RouterOSJobTarget {
  id: string
  device_id: string
  device_name: string
  status: 'pending' | 'running' | 'success' | 'failed' | 'skipped'
  output: string
  error: string
  before_state: Record<string, unknown>
  after_state: Record<string, unknown>
  started_at: string | null
  finished_at: string | null
}

export interface RouterOSJob {
  id: string
  name: string
  template_name: string
  status: 'pending_approval' | 'approved' | 'running' | 'completed' | 'failed' | 'cancelled'
  requires_approval: boolean
  targets: number
  created_by: string
  approved_by: string
  approved_at: string | null
  started_at: string | null
  finished_at: string | null
  created_at: string
  stats?: Record<string, unknown>
  success?: number
  failed?: number
  content?: string
  notes?: string
  plan?: RouterOSPlanItem[]
  target_device_ids?: string[]
  target_rows?: RouterOSJobTarget[]
}

export interface TrapRule {
  id: string
  name: string
  priority: number
  match_oid: string
  match_name: string
  severity: 'info' | 'ok' | 'warning' | 'critical'
  event_type: string
  message_template: string
  filter_device_ids: string[]
  filter_tags: string[]
  filter_map_ids: string[]
  channel_ids: string[]
  notify: boolean
  suppress: boolean
  is_enabled: boolean
}

export interface TrapVariable {
  oid: string
  name: string
  type: string
  value: string
}

export interface TrapEvent {
  id: number
  at: string
  source_ip: string
  device_id: string
  device_name: string
  map_id: string
  map_name: string
  trap_oid: string
  trap_name: string
  severity: string
  event_type: string
  message: string
  version: string
  variables: TrapVariable[]
  suppressed: boolean
  root_cause_device_id: string
  root_cause: string
  matched_rule_id: string
  matched_rule: string
}

export interface MIBNotification {
  module: string
  oid: string
  name: string
  description: string
  objects: string[]
}

export interface DudeMap {
  source_id: string
  name: string
  parent_source_id: string
  device_count: number
}

export interface DudeSNMPProfile {
  source_id: string
  name: string
  version: string
  port: number
  timeout_ms: number
  retries: number
  has_community: boolean
}

export interface DudeDevice {
  source_id: string
  node_id: string
  name: string
  address: string
  dns_name: string
  mac_address: string
  vendor: string
  model: string
  device_type: string
  notes: string
  source_map_id: string
  map_name: string
  pos_x: number
  pos_y: number
  width: number
  height: number
  tags: string[]
  username?: string
  has_password?: boolean
  snmp_profile_source_id?: string
  snmp_profile_name?: string
}

export interface DudeLink {
  source_id: string
  source_map_id: string
  node_a: string
  node_b: string
  device_a: string
  device_b: string
  link_type: string
  max_speed_bps: number
  mastering_type?: number
  master_device?: string
  master_interface?: number
}

export interface DudeSubmap {
  source_id: string
  map_id: string
  target_source_id: string
  name: string
  pos_x: number
  pos_y: number
  width: number
  height: number
}

export interface DudeDecoration {
  source_id: string
  map_id: string
  name: string
  pos_x: number
  pos_y: number
  width: number
  height: number
}

export interface DudePreview {
  source_kind: string
  filename: string
  maps: DudeMap[]
  devices: DudeDevice[]
  submaps: DudeSubmap[]
  decorations: DudeDecoration[]
  links: DudeLink[]
  snmp_profiles?: DudeSNMPProfile[]
  warnings: string[]
}

export interface DudeImport {
  id: string
  filename: string
  source_kind: string
  status: string
  device_count: number
  map_count: number
  link_count: number
  created_by: string
  created_at: string
  applied_at: string | null
  result?: Record<string, unknown>
  preview?: DudePreview
  warnings?: string[]
}

export interface ReportSummary {
  from: string
  to: string
  devices: number
  up: number
  warning: number
  down: number
  unreachable: number
  disabled: number
  unknown: number
  incidents: number
  active_outages: number
  downtime_sec: number
  availability_pct: number
  mttr_sec: number
  maxtime_sec: number
  top_down: ReportDevice[]
}

export interface ReportDevice {
  device_id: string
  device_name: string
  primary_ip: string
  status: Status
  device_type: string
  tags: string[]
  incidents: number
  downtime_sec: number
  availability_pct: number
  root_cause_device_id: string
  root_cause: string
}

export interface ReportClient {
  id: string
  name: string
  description: string
  target_pct: number
  devices: number
  incidents: number
  downtime_sec: number
  availability_pct: number
  compliant: boolean
}

export interface SLAClient {
  id: string
  name: string
  description: string
  device_ids: string[]
  map_ids: string[]
  tags: string[]
  target_pct: number
  created_at?: string
  updated_at?: string
}

export interface ReportInterface {
  link_id: string
  map_id: string
  map_name: string
  device_name: string
  interface_name: string
  link_type: string
  link_status: string
  max_speed_bps: number
  samples: number
  up_samples: number
  availability_pct: number
  avg_rx_bps: number
  avg_tx_bps: number
  max_errors: number
}

export interface ReportMonthly {
  month: string
  from: string
  to: string
  devices: number
  incidents: number
  downtime_sec: number
  availability_pct: number
  delta_pct: number
  delta_incidents: number
}

export interface ReportSchedule {
  id: string
  name: string
  report_type: 'sla' | 'interfaces' | 'executive' | 'clients'
  format: 'pdf' | 'csv'
  frequency: 'daily' | 'weekly' | 'monthly'
  hour_utc: number
  day_of_week: number
  day_of_month: number
  range_days: number
  map_id: string
  client_id: string
  include_interfaces: boolean
  export_external: boolean
  is_enabled: boolean
  last_run_at: string | null
  next_run_at: string
  created_by: string
  created_at: string
  updated_at: string
}

export interface ReportRun {
  id: string
  schedule_id: string
  report_type: string
  format: string
  status: string
  period_from: string
  period_to: string
  filename: string
  path: string
  size_bytes: number
  external_url: string
  error: string
  generated_by: string
  created_at: string
  finished_at: string | null
}

export interface ReportStorageConfig {
  type: string
  endpoint: string
  region: string
  bucket: string
  prefix: string
  access_key: string
  secret_key: string
  path: string
  public_base_url: string
  path_style: boolean
}

export interface ReportStorageResponse {
  config: ReportStorageConfig
  has_secret: boolean
  report_dir: string
}
