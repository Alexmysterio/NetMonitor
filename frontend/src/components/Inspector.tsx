import { useEffect, useMemo, useState } from 'react'
import {
  Activity,
  ArrowDown,
  ArrowUp,
  Check,
  ChevronRight,
  Circle,
  Clock3,
  Database,
  Diamond,
  ExternalLink,
  Gauge,
  Hexagon,
  Network,
  Pencil,
  Plus,
  Radar,
  RefreshCcw,
  Search,
  Square,
  Terminal,
  Trash2,
  Wrench,
  X,
} from 'lucide-react'
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { apiGet, apiSend } from '../api'
import type { CanvasData, CanvasElement, CanvasLink, Device, DeviceService, SnmpProfile, Shape } from '../types'
import { Button, deviceTypeLabel, EmptyState, Field, formatBps, formatDuration, IconButton, KeyValue, Modal, StatusBadge, StatusDot, timeAgo, timeHM, statusClass } from '../ui'
import SnmpInfoModal from './SnmpInfoModal'
import SnmpInfoSections from './SnmpInfoSections'
import NodeIcon from './NodeIcon'
import ToolsModal from './ToolsModal'
import { confirmAction } from '../confirm'

const SHAPES: { id: Shape; label: string; icon: React.ReactNode }[] = [
  { id: 'rounded_rect', label: 'Rect.', icon: <Square size={13} /> },
  { id: 'circle', label: 'Círculo', icon: <Circle size={13} /> },
  { id: 'square', label: 'Cuadro', icon: <Square size={13} /> },
  { id: 'pill', label: 'Cápsula', icon: <span className="shape-pill-icon small" /> },
  { id: 'diamond', label: 'Rombo', icon: <Diamond size={13} /> },
  { id: 'hexagon', label: 'Hex.', icon: <Hexagon size={13} /> },
]

function MetricChart({ data, kind }: { data: any[]; kind: 'latency' | 'traffic' }) {
  if (!data.length) {
    return <div className="chart-empty">Sin datos históricos todavía.<br /><span>El poller irá rellenando la serie temporal.</span></div>
  }
  return (
    <div className="mini-chart">
      <ResponsiveContainer width="100%" height={130}>
        <AreaChart data={data} margin={{ top: 6, right: 10, bottom: 0, left: 0 }}>
          <defs>
            <linearGradient id="chartFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#2563eb" stopOpacity={0.22} />
              <stop offset="100%" stopColor="#2563eb" stopOpacity={0.01} />
            </linearGradient>
            <linearGradient id="trafficFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#0891b2" stopOpacity={0.22} />
              <stop offset="100%" stopColor="#0891b2" stopOpacity={0.01} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke="#eef2f7" vertical={false} />
          <XAxis dataKey="time" hide />
          <YAxis tick={{ fontSize: 9, fill: '#94a3b8' }} axisLine={false} tickLine={false} width={kind === 'traffic' ? 58 : 42} tickFormatter={v => kind === 'traffic' ? formatBps(Number(v)) : `${Number(v).toFixed(0)}`} />
          <Tooltip
            contentStyle={{ borderRadius: 10, border: '1px solid #e2e8f0', fontSize: 11, boxShadow: '0 8px 24px rgba(15,23,42,.08)' }}
            labelFormatter={v => new Date(v).toLocaleString('es-ES')}
            formatter={(value: any, name: any) => [kind === 'traffic' ? formatBps(Number(value)) : `${Number(value).toFixed(1)} ms`, name === 'tx_bps' ? 'TX' : name === 'rx_bps' ? 'RX' : 'Latencia']}
          />
          {kind === 'traffic' ? (
            <>
              <Area type="monotone" dataKey="rx_bps" stroke="#0891b2" fill="url(#trafficFill)" strokeWidth={1.8} dot={false} />
              <Area type="monotone" dataKey="tx_bps" stroke="#7c3aed" fill="transparent" strokeWidth={1.4} dot={false} />
            </>
          ) : (
            <Area type="monotone" dataKey="rtt_ms" stroke="#2563eb" fill="url(#chartFill)" strokeWidth={1.8} dot={false} />
          )}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}

function MiscElementInspector({
  mapId,
  element,
  onRefresh,
  onClose,
}: {
  mapId: string
  element: CanvasElement
  onRefresh: () => void
  onClose: () => void
}) {
  const isZone = element.element_type === 'zone'
  const isNetwork = element.element_type === 'network'
  const [label, setLabel] = useState(element.label_template)
  const [cidr, setCidr] = useState(element.network_cidr || '')
  const [width, setWidth] = useState(Math.round(element.width))
  const [height, setHeight] = useState(Math.round(element.height))
  const [rotation, setRotation] = useState(Math.round(Number(element.rotation || 0)))
  const [color, setColor] = useState(String((element.custom_style as any)?.color || (isNetwork ? '#0f766e' : '#94a3b8')))
  const [fill, setFill] = useState(String((element.custom_style as any)?.fill || (isNetwork ? '#ecfdf5' : '#f8fafc')))
  const [saving, setSaving] = useState(false)

  const save = async () => {
    setSaving(true)
    try {
      const style = isZone ? { color, fill, dashed: true } : isNetwork ? { color, fill } : { color, fontSize: 12 }
      await apiSend(`/maps/${mapId}/elements/${element.id}`, 'PATCH', {
        label_template: label,
        width,
        height,
        rotation,
        ...(isNetwork ? { network_cidr: cidr } : {}),
        custom_style: style,
      })
      onRefresh()
    } finally {
      setSaving(false)
    }
  }

  const remove = async () => {
    if (!await confirmAction('¿Quitar este elemento del mapa?')) return
    await apiSend(`/maps/${mapId}/elements/${element.id}`, 'DELETE')
    onClose()
    onRefresh()
  }

  return (
    <div className="inspector-inner">
      <div className="inspector-head">
        <div className={`inspector-icon ${isZone ? 'neutral' : ''}`}>{isNetwork ? <Network size={18} /> : isZone ? '▦' : 'T'}</div>
        <div className="inspector-title">
          <span className="eyebrow">{isZone ? 'Zona visual' : isNetwork ? 'Red / subred' : 'Texto libre'}</span>
          <h2>{isZone || isNetwork ? label : 'Etiqueta de texto'}</h2>
          <div className="inspector-subline"><span>{isNetwork ? element.network_cidr || 'Sin CIDR' : 'Elemento de lienzo'}</span></div>
        </div>
        <IconButton onClick={onClose} label="Cerrar"><X size={16} /></IconButton>
      </div>
      <div className="inspector-body">
        {isNetwork && (
          <>
            <Field label="Subred / CIDR" hint="El estado se calcula con los dispositivos cuya IP principal pertenece a la red.">
              <input className="input mono" value={cidr} onChange={e => setCidr(e.target.value)} placeholder="192.0.2.0/24" />
            </Field>
            <div className="inspector-summary-grid four">
              <div><b className="ok">{element.network?.counts?.up ?? 0}</b><span>Up</span></div>
              <div><b className="warn">{element.network?.counts?.warning ?? 0}</b><span>Warn</span></div>
              <div><b className="danger">{element.network?.counts?.down ?? 0}</b><span>Down</span></div>
              <div><b className="violet">{element.network?.counts?.unreachable ?? 0}</b><span>Aislados</span></div>
            </div>
          </>
        )}
        <Field label={isZone ? 'Nombre de la zona' : isNetwork ? 'Etiqueta de red' : 'Texto'} hint={isNetwork ? 'Admite [Network.CIDR], [Network.Summary], [Network.DevicesUp]…' : undefined}>
          <textarea className="input textarea" rows={isZone ? 2 : isNetwork ? 3 : 4} value={label} onChange={e => setLabel(e.target.value)} />
        </Field>
        <div className="inline-fields">
          <Field label="Ancho"><input className="input" type="number" value={width} onChange={e => setWidth(Number(e.target.value))} /></Field>
          <Field label="Alto"><input className="input" type="number" value={height} onChange={e => setHeight(Number(e.target.value))} /></Field>
          <Field label="Rotación°"><input className="input" type="number" min={-180} max={180} value={rotation} onChange={e => setRotation(Number(e.target.value))} /></Field>
        </div>
        <div className="inline-fields">
          <Field label={isZone || isNetwork ? 'Borde' : 'Color'}>
            <input className="input color-input" type="color" value={color} onChange={e => setColor(e.target.value)} />
          </Field>
          {(isZone || isNetwork) && <Field label="Relleno"><input className="input color-input" type="color" value={fill} onChange={e => setFill(e.target.value)} /></Field>}
        </div>
        <Button variant="primary" onClick={save} disabled={saving}><Check size={15} /> {saving ? 'Guardando…' : 'Aplicar cambios'}</Button>
        <div className="inspector-danger"><Button variant="danger" size="sm" onClick={remove}><Trash2 size={14} /> Quitar del mapa</Button></div>
      </div>
    </div>
  )
}

function ElementInspector({
  mapId,
  element,
  onRefresh,
  onClose,
  onNavigate,
  refreshKey,
}: {
  mapId: string
  element: CanvasElement
  onRefresh: () => void
  onClose: () => void
  onNavigate: (id: string) => void
  refreshKey: number
}) {
  const [device, setDevice] = useState<Device | null>(null)
  const [tab, setTab] = useState<'general' | 'services' | 'snmp' | 'label'>('general')
  const [shape, setShape] = useState<Shape>(element.shape)
  const [label, setLabel] = useState(element.label_template)
  const [zoneID, setZoneID] = useState(element.zone_id || '')
  const [zones, setZones] = useState<any[]>([])
  const [width, setWidth] = useState(Math.round(element.width))
  const [height, setHeight] = useState(Math.round(element.height))
  const [saving, setSaving] = useState(false)
  const [selectedService, setSelectedService] = useState<string>('')
  const [metrics, setMetrics] = useState<any[]>([])
  const [probes, setProbes] = useState<any[]>([])
  const [deviceTypes, setDeviceTypes] = useState<any[]>([])
  const [newProbe, setNewProbe] = useState('')
  const [walkOpen, setWalkOpen] = useState(false)
  const [walkOid, setWalkOid] = useState('1.3.6.1.2.1.1')
  const [walkRows, setWalkRows] = useState<any[]>([])
  const [walkLoading, setWalkLoading] = useState(false)
  const [walkError, setWalkError] = useState('')
  const [snmpOpen, setSnmpOpen] = useState(false)
  const [deviceEditOpen, setDeviceEditOpen] = useState(false)
  const [toolsOpen, setToolsOpen] = useState(false)
  const [snmpProfiles, setSnmpProfiles] = useState<SnmpProfile[]>([])
  const [agents, setAgents] = useState<any[]>([])
  const [allDevices, setAllDevices] = useState<Device[]>([])
  const [impact, setImpact] = useState<any>(null)
  const [deviceForm, setDeviceForm] = useState({
    name: '', primary_ip: '', secondary_ips: '', dns_name: '', device_type: 'router', snmp_profile_id: '', agent_id: '',
    tags: '', notes: '', parent_ids: [] as string[], monitoring: true,
    poll_interval_sec: 0, poll_down_retries: 0, poll_agent_id: '', poll_notifications: -1,
  })

  useEffect(() => {
    apiGet<any>(`/maps/${mapId}/canvas`).then((data: any) => {
      setZones((data.elements || []).filter((e: any) => e.element_type === 'zone'))
    }).catch(() => {})
  }, [mapId])

  useEffect(() => {
    setShape(element.shape)
    setLabel(element.label_template)
    setZoneID(element.zone_id || '')
    setWidth(Math.round(element.width))
    setHeight(Math.round(element.height))
  }, [element.id, element.shape, element.label_template, element.width, element.height])

  useEffect(() => {
    if (!element.device_id) {
      setDevice(null)
      return
    }
    apiGet<Device>(`/devices/${element.device_id}`).then(d => {
      setDevice(d)
      const ping = d.services.find(s => s.probe_type === 'icmp') || d.services[0]
      if (ping) setSelectedService(ping.id)
    }).catch(() => setDevice(null))
  }, [element.device_id, element.status, refreshKey])

  useEffect(() => {
    if (!element.device_id) {
      setImpact(null)
      return
    }
    apiGet<any>(`/correlation/impact/${element.device_id}`).then(setImpact).catch(() => setImpact(null))
  }, [element.device_id, refreshKey])

  useEffect(() => {
    if (element.device_id && tab === 'services' && probes.length === 0) {
      apiGet<any[]>('/probes').then(setProbes).catch(() => {})
    }
  }, [tab, element.device_id, probes.length])

  useEffect(() => {
    if (deviceTypes.length === 0) apiGet<any[]>('/device-types').then(setDeviceTypes).catch(() => {})
  }, [deviceTypes.length])

  useEffect(() => {
    if (!selectedService) {
      setMetrics([])
      return
    }
    apiGet<any[]>(`/metrics/service/${selectedService}?range=24h`).then(setMetrics).catch(() => setMetrics([]))
  }, [selectedService, refreshKey])

  const saveElement = async (patch: Record<string, unknown> = {}) => {
    setSaving(true)
    try {
      await apiSend(`/maps/${mapId}/elements/${element.id}`, 'PATCH', {
        shape,
        label_template: label,
        width,
        height,
        zone_id: zoneID,
        ...patch,
      })
      onRefresh()
    } finally {
      setSaving(false)
    }
  }

  const applyTypeStyle = async () => {
    if (!device) return
    const meta = deviceTypes.find((t: any) => t.name === device.device_type)
    if (!meta) return
    setSaving(true)
    try {
      await apiSend(`/maps/${mapId}/elements/${element.id}`, 'PATCH', {
        icon_name: meta.icon || 'server',
        shape: meta.shape || element.shape,
        custom_style: { ...(element.custom_style || {}), color: meta.color || element.custom_style?.color || '' },
      })
      onRefresh()
    } finally {
      setSaving(false)
    }
  }

  const openDeviceEdit = async () => {
    if (!device) return
    setDeviceForm({
      name: device.name,
      primary_ip: device.primary_ip,
      secondary_ips: (device.secondary_ips || []).join(', '),
      dns_name: device.dns_name || '',
      device_type: device.device_type,
      snmp_profile_id: device.snmp_profile_id || '',
      agent_id: device.agent_id || '',
      tags: (device.tags || []).join(', '),
      notes: device.notes || '',
      parent_ids: device.parent_ids || [],
      monitoring: device.is_monitoring_enabled,
      poll_interval_sec: device.poll_interval_sec || 0,
      poll_down_retries: device.poll_down_retries || 0,
      poll_agent_id: device.poll_agent_id || '',
      poll_notifications: device.poll_notifications ?? -1,
    })
    const [profs, ags, devs] = await Promise.all([
      apiGet<SnmpProfile[]>('/snmp-profiles').catch(() => []),
      apiGet<any[]>('/agents').catch(() => []),
      apiGet<Device[]>('/devices').catch(() => []),
    ])
    setSnmpProfiles(profs)
    setAgents(ags)
    setAllDevices(devs.filter(d => d.id !== device.id))
    setDeviceEditOpen(true)
  }

  const saveDeviceEdit = async () => {
    if (!element.device_id) return
    setSaving(true)
    try {
      await apiSend(`/devices/${element.device_id}`, 'PUT', {
        ...deviceForm,
        secondary_ips: deviceForm.secondary_ips.split(',').map(v => v.trim()).filter(Boolean),
        snmp_profile_id: deviceForm.snmp_profile_id || '',
        agent_id: deviceForm.agent_id || '',
        tags: deviceForm.tags.split(',').map(t => t.trim()).filter(Boolean),
        parent_ids: deviceForm.parent_ids,
        is_monitoring_enabled: deviceForm.monitoring,
      })
      const updated = await apiGet<Device>(`/devices/${element.device_id}`)
      setDevice(updated)
      setDeviceEditOpen(false)
      onRefresh()
    } finally {
      setSaving(false)
    }
  }

  const removeElement = async () => {
    if (!await confirmAction('¿Quitar este elemento del mapa? El dispositivo no se borrará del inventario.')) return
    await apiSend(`/maps/${mapId}/elements/${element.id}`, 'DELETE')
    onClose()
    onRefresh()
  }

  const ackDevice = async () => {
    if (!element.device_id) return
    if (element.device?.is_acknowledged) {
      await apiSend(`/devices/${element.device_id}/acknowledge`, 'DELETE')
    } else {
      await apiSend(`/devices/${element.device_id}/acknowledge`, 'POST', { note: 'Reconocido desde el mapa', duration_minutes: 0 })
    }
    window.dispatchEvent(new Event('netmonitor:maps-changed'))
    onRefresh()
    if (device) setDevice({ ...device, is_acknowledged: !device.is_acknowledged })
  }

  const setMaintenance = async () => {
    if (!element.device_id) return
    const raw = prompt('Duración del mantenimiento en minutos (0 para finalizar):', '60')
    if (raw === null) return
    const minutes = Number(raw)
    if (Number.isNaN(minutes)) return
    await apiSend(`/devices/${element.device_id}/maintenance`, 'POST', { minutes, note: minutes > 0 ? 'Mantenimiento programado' : '' })
    onRefresh()
  }

  const addService = async () => {
    if (!newProbe || !element.device_id) return
    await apiSend(`/devices/${element.device_id}/services`, 'POST', { probe_id: newProbe, interval_sec: 30 })
    setNewProbe('')
    onRefresh()
    const d = await apiGet<Device>(`/devices/${element.device_id}`)
    setDevice(d)
  }

  const runWalk = async () => {
    if (!element.device_id) return
    setWalkLoading(true)
    setWalkError('')
    try {
      const rows = await apiSend<any[]>(`/tools/snmp-walk`, 'POST', { device_id: element.device_id, oid: walkOid })
      setWalkRows(rows)
    } catch (e) {
      setWalkError((e as Error).message)
    } finally {
      setWalkLoading(false)
    }
  }

  const isSubmap = element.element_type === 'submap'
  const status = element.status
  const activeService = device?.services.find(s => s.id === selectedService)
  const previewLabel = (label || element.label_template || '[Device.Name]\n[Device.FirstAddress]')
    .replace(/\[Device\.Name\]/g, device?.name || 'Dispositivo')
    .replace(/\[Device\.FirstAddress\]/g, device?.primary_ip || '')
    .replace(/\[Device\.StatusText\]/g, element.status_text || '')
    .replace(/\[Submap\.Name\]/g, element.submap?.name || '')
    .replace(/\[Submap\.Summary\]/g, element.submap ? `${element.submap.counts.up} up · ${element.submap.counts.warning} warn · ${element.submap.counts.down} down${(element.submap.counts.acknowledged || 0) > 0 ? ` · ${element.submap.counts.acknowledged} ack` : ''}` : '')
  const previewLines = previewLabel.split('\n').filter(Boolean)
  const previewShape = shape === 'circle'
    ? { borderRadius: '50%' }
    : shape === 'pill'
      ? { borderRadius: '999px' }
      : shape === 'square'
        ? { borderRadius: '4px' }
        : shape === 'diamond'
          ? { clipPath: 'polygon(50% 0, 100% 50%, 50% 100%, 0 50%)' }
          : shape === 'hexagon'
            ? { clipPath: 'polygon(25% 0, 75% 0, 100% 50%, 75% 100%, 25% 100%, 0 50%)' }
            : { borderRadius: '10px' }

  return (
    <div className="inspector-inner">
      <div className="inspector-head">
        <div className={`inspector-icon ${statusClass(status)}`}>{isSubmap ? '🗺️' : '🖥️'}</div>
        <div className="inspector-title">
          <span className="eyebrow">{isSubmap ? 'Submapa' : element.device?.custom_fields?.site as string || 'Dispositivo'}</span>
          <h2>{isSubmap ? element.submap?.name : element.device?.name || 'Nodo'}</h2>
          <div className="inspector-subline">
            <StatusDot status={status} />
            <span>{element.status_text}</span>
            {element.device?.rtt_ms ? <span className="mono muted">· {element.device.rtt_ms.toFixed(1)} ms</span> : null}
          </div>
        </div>
        <IconButton onClick={onClose} label="Cerrar"><X size={16} /></IconButton>
      </div>

      {isSubmap ? (
        <div className="inspector-body">
          <div className="inspector-summary-grid">
            <div><b className="ok">{element.submap?.counts.up ?? 0}</b><span>Operativos</span></div>
            <div><b className="warn">{element.submap?.counts.warning ?? 0}</b><span>Avisos</span></div>
            <div><b className="danger">{element.submap?.counts.down ?? 0}</b><span>Caídos</span></div>
            <div><b className="ack">{element.submap?.counts.acknowledged ?? 0}</b><span>Reconocidos</span></div>
            <div><b className="violet">{element.submap?.counts.unreachable ?? 0}</b><span>Aislados</span></div>
          </div>
          <Button variant="primary" onClick={() => element.target_submap_id && onNavigate(element.target_submap_id)}>
            <ExternalLink size={15} /> Abrir submapa
          </Button>
          <div className="section-label">Apariencia</div>
          <ShapeSelector value={shape} onChange={setShape} />
          <Field label="Zona lógica" hint="El nodo se moverá con la zona.">
            <select className="input" value={zoneID} onChange={e => setZoneID(e.target.value)}>
              <option value="">Sin zona</option>
              {zones.map((z: any) => <option key={z.id} value={z.id}>{z.rendered_label || z.label_template || 'Zona'}</option>)}
            </select>
          </Field>
          <Field label="Etiqueta del submapa">
            <textarea className="input textarea" value={label} onChange={e => setLabel(e.target.value)} rows={3} />
          </Field>
          <div className="inline-fields">
            <Field label="Ancho"><input className="input" type="number" value={width} onChange={e => setWidth(Number(e.target.value))} /></Field>
            <Field label="Alto"><input className="input" type="number" value={height} onChange={e => setHeight(Number(e.target.value))} /></Field>
          </div>
          <Button variant="primary" onClick={() => saveElement()} disabled={saving}><Check size={15} /> {saving ? 'Guardando…' : 'Guardar cambios'}</Button>
          <Button variant="danger" size="sm" onClick={removeElement}><Trash2 size={14} /> Quitar del mapa</Button>
        </div>
      ) : (
        <>
          <div className="inspector-tabs">
            <button className={tab === 'general' ? 'active' : ''} onClick={() => setTab('general')}>General</button>
            <button className={tab === 'services' ? 'active' : ''} onClick={() => setTab('services')}>Sondas {device?.services.length ? <span>{device.services.length}</span> : null}</button>
            {device?.snmp_profile_id && <button className={tab === 'snmp' ? 'active' : ''} onClick={() => setTab('snmp')}>SNMP</button>}
            <button className={tab === 'label' ? 'active' : ''} onClick={() => setTab('label')}>Apariencia</button>
          </div>

          {tab === 'general' && (
            <div className="inspector-body">
              <div className="status-callout">
                <div className={`callout-bar ${statusClass(status)}`} />
                <div>
                  <b>{element.status_text}</b>
                  <p>{element.device?.status_reason || 'Sin incidencias registradas'}</p>
                </div>
                <span className="mono small">{element.device?.rtt_ms ? `${element.device.rtt_ms.toFixed(2)} ms` : '—'}</span>
              </div>

              {impact?.impact && (impact.resolution?.has_root || (impact.impact.impacted || []).length > 0) && (
                <div className="correlation-callout">
                  <div>
                    <span>Causa raíz</span>
                    <b>{impact.impact.root?.name || element.device?.root_cause || 'Aguas arriba'}</b>
                  </div>
                  <div>
                    <span>Impacto</span>
                    <b>{(impact.impact.impacted || []).length} equipos</b>
                  </div>
                  <div className="correlation-hops">
                    <span>{impact.impact.parent_hops ? `${impact.impact.parent_hops} saltos Parents` : 'Topología L2'}</span>
                  </div>
                </div>
              )}

              <div className="quick-actions">
                <button onClick={ackDevice} className={element.device?.is_acknowledged ? 'active' : ''}><Check size={14} /> {element.device?.is_acknowledged ? 'Ack activo' : 'Reconocer'}</button>
                <button onClick={() => apiSend(`/devices/${element.device_id}/poll-now`, 'POST').then(onRefresh)}><RefreshCcw size={14} /> Sondear</button>
                <button onClick={async () => { const r = await apiSend<any>('/tools/ping', 'POST', { host: element.device?.primary_ip, count: 4 }); alert(`${r.host}\n${r.received}/${r.sent} respuestas · ${r.avg_ms?.toFixed?.(1) ?? r.avg_ms} ms`) }}><Activity size={14} /> Ping</button>
                <button onClick={() => setToolsOpen(true)}><Wrench size={14} /> Herramientas</button>
                <button onClick={() => setSnmpOpen(true)} disabled={!device?.snmp_profile_id} title={device?.snmp_profile_id ? 'Inventario SNMP en vivo' : 'Sin perfil SNMP'}><Database size={14} /> Analizar SNMP</button>
                <button onClick={() => setWalkOpen(true)} disabled={!device?.snmp_profile_id} title={device?.snmp_profile_id ? 'Explorar OIDs' : 'Sin perfil SNMP'}><Radar size={14} /> SNMP Walk</button>
                <button onClick={setMaintenance}><Clock3 size={14} /> Mantenimiento</button>
              </div>

              <div className="section-label">Identidad</div>
              <KeyValue k="Dirección IP" v={element.device?.primary_ip || '—'} mono />
              {device?.secondary_ips?.length ? <KeyValue k="IPs secundarias" v={device.secondary_ips.join(', ')} mono /> : null}
              <KeyValue k="Tipo" v={device ? deviceTypeLabel(device.device_type) : '—'} />
              {device && <Button variant="secondary" size="sm" onClick={applyTypeStyle} disabled={saving}>Aplicar icono, forma y color del tipo</Button>}
              <KeyValue k="MAC" v={device?.mac_address || '—'} mono />
              {device && <Button variant="secondary" size="sm" onClick={openDeviceEdit}><Pencil size={13} /> Modificar dispositivo</Button>}
              <KeyValue k="Última vez visto" v={device ? timeAgo(device.last_seen_up_at) : '—'} />
              <KeyValue k="Etiquetas" v={device?.tags?.length ? device.tags.map(t => <span key={t} className="mini-tag">{t}</span>) : '—'} />
              {device?.maintenance_until && <KeyValue k="Mantenimiento" v={new Date(device.maintenance_until).toLocaleString('es-ES')} />}
              {device?.notes && <div className="notes-box">{device.notes}</div>}

              <div className="section-label">Servicios</div>
              {device?.services.slice(0, 4).map(s => (
                <div key={s.id} className="mini-service-row">
                  <StatusDot status={s.status} />
                  <span>{s.probe_name}</span>
                  <span className="mono muted">{s.last_value ? `${s.last_value.toFixed(1)}${s.unit}` : '—'}</span>
                </div>
              ))}
              {!device?.services.length && <div className="muted small">Sin sondas asignadas.</div>}

              <div className="inspector-danger">
                <Button variant="danger" size="sm" onClick={removeElement}><Trash2 size={14} /> Quitar del mapa</Button>
              </div>
            </div>
          )}

          {tab === 'services' && (
            <div className="inspector-body">
              <div className="service-list">
                {device?.services.map((s: DeviceService) => (
                  <button key={s.id} className={`service-row ${selectedService === s.id ? 'active' : ''}`} onClick={() => setSelectedService(s.id)}>
                    <StatusDot status={s.status} />
                    <div className="service-row-main">
                      <b>{s.name || s.probe_name}</b>
                      <span>{s.last_output_message || 'Sin respuesta'}</span>
                    </div>
                    <div className="service-row-value">
                      {s.last_value ? `${s.last_value.toFixed(1)}` : '—'}<small>{s.unit}</small>
                    </div>
                    <ChevronRight size={13} className="muted" />
                  </button>
                ))}
                {!device?.services.length && <EmptyState title="Sin sondas" message="Añade la primera sonda para empezar a monitorizar este equipo." icon={<Activity size={22} />} />}
              </div>

              {activeService && (
                <>
                  <div className="chart-head"><span>Últimas 24 h · {activeService.probe_name}</span><span className="mono muted">{activeService.interval_sec}s</span></div>
                  <MetricChart data={metrics} kind="latency" />
                </>
              )}

              <div className="add-service-row">
                <select className="input" value={newProbe} onChange={e => setNewProbe(e.target.value)}>
                  <option value="">Añadir sonda…</option>
                  {probes.filter(p => !device?.services.some(s => s.probe_id === p.id)).map(p => (
                    <option key={p.id} value={p.id}>{p.name} · {p.probe_type}</option>
                  ))}
                </select>
                <Button variant="secondary" onClick={addService} disabled={!newProbe}><Plus size={14} /></Button>
              </div>
            </div>
          )}

          {tab === 'label' && (
            <div className="inspector-body">
              <div className="section-label">Forma geométrica</div>
              <ShapeSelector value={shape} onChange={setShape} />
              <Field label="Zona lógica" hint="El nodo se moverá con la zona.">
                <select className="input" value={zoneID} onChange={e => setZoneID(e.target.value)}>
                  <option value="">Sin zona</option>
                  {zones.map((z: any) => <option key={z.id} value={z.id}>{z.rendered_label || z.label_template || 'Zona'}</option>)}
                </select>
              </Field>
              <Field label="Plantilla de etiqueta" hint="Usa [Device.Name], [Device.FirstAddress], [Device.Addresses], [Device.SecondaryAddresses] o [funcion()] sobre los OIDs cacheados.">
                <textarea className="input textarea mono" value={label} onChange={e => setLabel(e.target.value)} rows={5} />
              </Field>
              <div className="inline-fields">
                <Field label="Ancho"><input className="input" type="number" value={width} onChange={e => setWidth(Number(e.target.value))} /></Field>
                <Field label="Alto"><input className="input" type="number" value={height} onChange={e => setHeight(Number(e.target.value))} /></Field>
              </div>
              <div className="label-preview">
                <span className="field-label">Vista previa</span>
                <div className={`preview-node ${statusClass(status)}`} style={{ width: Math.min(width, 250), height: Math.min(height, 120), ...previewShape }}>
                  <div className="preview-icon"><NodeIcon name={element.icon_name} size={34} /></div>
                  <b>{previewLines[0] || 'Nodo'}</b>
                  <span>{previewLines.slice(1).join(' · ')}</span>
                </div>
              </div>
              <Button variant="primary" onClick={() => saveElement()} disabled={saving}><Check size={15} /> {saving ? 'Guardando…' : 'Aplicar cambios'}</Button>
            </div>
          )}
          {tab === 'snmp' && (
            <div className="inspector-body">
              <SnmpInfoSections device={device} />
            </div>
          )}
        </>
      )}

      <Modal open={deviceEditOpen} onClose={() => setDeviceEditOpen(false)} title="Modificar dispositivo" subtitle={device?.primary_ip} width={680}
        footer={<><Button variant="ghost" onClick={() => setDeviceEditOpen(false)}>Cancelar</Button><Button variant="primary" onClick={saveDeviceEdit} disabled={saving}>{saving ? 'Guardando…' : 'Guardar cambios'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={deviceForm.name} onChange={e => setDeviceForm({ ...deviceForm, name: e.target.value })} /></Field>
          <Field label="IP / host"><input className="input mono" value={deviceForm.primary_ip} onChange={e => setDeviceForm({ ...deviceForm, primary_ip: e.target.value })} /></Field>
          <Field label="IPs secundarias" hint="Separadas por comas. Se usan en búsqueda y fichas."><input className="input mono" value={deviceForm.secondary_ips} onChange={e => setDeviceForm({ ...deviceForm, secondary_ips: e.target.value })} placeholder="192.0.2.2, 192.0.2.3" /></Field>
          <Field label="DNS"><input className="input" value={deviceForm.dns_name} onChange={e => setDeviceForm({ ...deviceForm, dns_name: e.target.value })} /></Field>
          <Field label="Tipo"><select className="input" value={deviceForm.device_type} onChange={e => setDeviceForm({ ...deviceForm, device_type: e.target.value })}>{deviceTypes.map((t: any) => <option key={t.name} value={t.name}>{t.label}</option>)}</select></Field>
          <Field label="Perfil SNMP"><select className="input" value={deviceForm.snmp_profile_id} onChange={e => setDeviceForm({ ...deviceForm, snmp_profile_id: e.target.value })}><option value="">— Sin SNMP</option>{snmpProfiles.map(p => <option key={p.id} value={p.id}>{p.name} · {p.version}</option>)}</select></Field>
          <Field label="Agente de sondeo"><select className="input" value={deviceForm.agent_id} onChange={e => setDeviceForm({ ...deviceForm, agent_id: e.target.value })}><option value="">— Servidor central</option>{agents.map((a: any) => <option key={a.id} value={a.id}>{a.name}{a.online ? ' · online' : ' · offline'}</option>)}</select></Field>
          <Field label="Override polling (s)"><input className="input" type="number" min={0} value={deviceForm.poll_interval_sec} onChange={e => setDeviceForm({ ...deviceForm, poll_interval_sec: Number(e.target.value) })} /></Field>
          <Field label="Override reintentos"><input className="input" type="number" min={0} value={deviceForm.poll_down_retries} onChange={e => setDeviceForm({ ...deviceForm, poll_down_retries: Number(e.target.value) })} /></Field>
          <Field label="Override agente"><select className="input" value={deviceForm.poll_agent_id} onChange={e => setDeviceForm({ ...deviceForm, poll_agent_id: e.target.value })}><option value="">— Heredar</option>{agents.map((a: any) => <option key={a.id} value={a.id}>{a.name}</option>)}</select></Field>
          <Field label="Override notificaciones"><select className="input" value={deviceForm.poll_notifications} onChange={e => setDeviceForm({ ...deviceForm, poll_notifications: Number(e.target.value) })}><option value={-1}>Heredar</option><option value={1}>Activadas</option><option value={0}>Silenciadas</option></select></Field>
          <Field label="Etiquetas" hint="Separadas por comas."><input className="input" value={deviceForm.tags} onChange={e => setDeviceForm({ ...deviceForm, tags: e.target.value })} /></Field>
        </div>
        <label className="check-label"><input type="checkbox" checked={deviceForm.monitoring} onChange={e => setDeviceForm({ ...deviceForm, monitoring: e.target.checked })} /> Monitorización activa</label>
        <Field label="Dependencias (Parents)" hint="Si todos los padres caen, este equipo pasa a Inalcanzable.">
          <div className="parent-picker">
            {allDevices.map(d => (
              <label key={d.id} className={`parent-option ${deviceForm.parent_ids.includes(d.id) ? 'active' : ''}`}>
                <input type="checkbox" checked={deviceForm.parent_ids.includes(d.id)} onChange={e => setDeviceForm({ ...deviceForm, parent_ids: e.target.checked ? [...deviceForm.parent_ids, d.id] : deviceForm.parent_ids.filter(id => id !== d.id) })} />
                <StatusDot status={d.status} />
                <span>{d.name}</span>
                <small className="mono">{d.primary_ip}</small>
              </label>
            ))}
            {!allDevices.length && <span className="muted small">No hay otros dispositivos.</span>}
          </div>
        </Field>
        <Field label="Notas"><textarea className="input textarea" rows={3} value={deviceForm.notes} onChange={e => setDeviceForm({ ...deviceForm, notes: e.target.value })} /></Field>
      </Modal>

      <SnmpInfoModal
        open={snmpOpen}
        onClose={() => setSnmpOpen(false)}
        deviceId={element.device_id}
        deviceName={device?.name}
        deviceIP={device?.primary_ip}
        onWalk={() => { setSnmpOpen(false); setWalkOpen(true) }}
      />

      <ToolsModal
        open={toolsOpen}
        onClose={() => setToolsOpen(false)}
        host={device?.primary_ip || element.device?.primary_ip || ''}
        name={device?.name || element.device?.name}
      />

      <Modal open={walkOpen} onClose={() => setWalkOpen(false)} title="SNMP Walk" subtitle="Explora el árbol OID del equipo en vivo." width={720}>
        <div className="walk-bar">
          <input className="input mono" value={walkOid} onChange={e => setWalkOid(e.target.value)} placeholder="1.3.6.1.2.1.1" />
          <Button variant="dark" onClick={runWalk} disabled={walkLoading}>{walkLoading ? 'Recorriendo…' : 'Recorrer'}</Button>
        </div>
        {walkError && <div className="form-error">{walkError}</div>}
        <div className="walk-table">
          <div className="walk-row head"><span>OID</span><span>Nombre MIB</span><span>Tipo</span><span>Valor</span></div>
          {walkRows.map((r, i) => (
            <div className="walk-row" key={i}><span className="mono">{r.oid}</span><span className="walk-name">{r.name || '—'}</span><span>{r.type}</span><span className="mono">{String(r.value)}</span></div>
          ))}
          {!walkRows.length && !walkLoading && <div className="walk-empty">Introduce un OID raíz y pulsa Recorrer.</div>}
        </div>
      </Modal>
    </div>
  )
}

function ShapeSelector({ value, onChange }: { value: Shape; onChange: (s: Shape) => void }) {
  return (
    <div className="shape-options compact">
      {SHAPES.map(s => (
        <button key={s.id} className={`shape-option ${value === s.id ? 'active' : ''}`} onClick={() => onChange(s.id)} title={s.label}>
          {s.icon}<span>{s.label}</span>
        </button>
      ))}
    </div>
  )
}

function EdgeInspector({
  mapId,
  link,
  elements,
  onRefresh,
  onClose,
  refreshKey,
}: {
  mapId: string
  link: CanvasLink
  elements: CanvasElement[]
  onRefresh: () => void
  onClose: () => void
  refreshKey: number
}) {
  const [metrics, setMetrics] = useState<any[]>([])
  const [editing, setEditing] = useState(false)
  const [linkType, setLinkType] = useState(link.link_type)
  const [maxSpeed, setMaxSpeed] = useState(Math.round(link.max_speed_bps / 1_000_000))
  const [warnPct, setWarnPct] = useState(link.warning_utilization_pct)
  const [critPct, setCritPct] = useState(link.critical_utilization_pct)
  const [colorByTraffic, setColorByTraffic] = useState(link.color_by_traffic !== false)
  const [mastering, setMastering] = useState(link.mastering_type)
  const [masterDevice, setMasterDevice] = useState(link.master_device_id || '')
  const [ifIndex, setIfIndex] = useState(link.snmp_if_index || 0)
  const [ifName, setIfName] = useState(link.snmp_if_name || '')
  const [devices, setDevices] = useState<Device[]>([])
  const [interfaces, setInterfaces] = useState<any[]>([])
  const [loadingIfaces, setLoadingIfaces] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    apiGet<any[]>(`/metrics/link/${link.id}?range=24h`).then(setMetrics).catch(() => setMetrics([]))
  }, [link.id, refreshKey])
  useEffect(() => {
    if (editing) apiGet<Device[]>('/devices').then(setDevices).catch(() => {})
  }, [editing])

  useEffect(() => {
    if (mastering !== 'snmp' || !masterDevice) {
      setInterfaces([])
      return
    }
    setLoadingIfaces(true)
    apiGet<any[]>(`/devices/${masterDevice}/interfaces`)
      .then(setInterfaces)
      .catch(() => setInterfaces([]))
      .finally(() => setLoadingIfaces(false))
  }, [mastering, masterDevice])

  const source = elements.find(e => e.id === link.source_element_id)
  const target = elements.find(e => e.id === link.target_element_id)

  const save = async () => {
    setSaving(true)
    try {
      await apiSend(`/maps/${mapId}/links/${link.id}`, 'PATCH', {
        link_type: linkType,
        max_speed_bps: maxSpeed * 1_000_000,
        warning_utilization_pct: warnPct,
        critical_utilization_pct: critPct,
        color_by_traffic: colorByTraffic,
        mastering_type: mastering,
        master_device_id: masterDevice || null,
        snmp_if_index: ifIndex,
        snmp_if_name: ifName,
      })
      setEditing(false)
      onRefresh()
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="inspector-inner">
      <div className="inspector-head">
        <div className={`inspector-icon ${statusClass(link.status)}`}><Gauge size={20} /></div>
        <div className="inspector-title">
          <span className="eyebrow">Enlace · {link.link_type}</span>
          <h2>{source?.device?.name || source?.submap?.name || 'Origen'} → {target?.device?.name || target?.submap?.name || 'Destino'}</h2>
          <div className="inspector-subline"><StatusDot status={link.status} /><span>{link.status === 'down' ? 'Enlace caído' : `${link.utilization_pct.toFixed(0)}% de utilización`}</span></div>
        </div>
        <IconButton onClick={onClose} label="Cerrar"><X size={16} /></IconButton>
      </div>
      <div className="inspector-body">
        <div className="traffic-cards">
          <div className="traffic-card tx"><span><ArrowUp size={13} /> TX</span><b>{formatBps(link.tx_bps)}</b></div>
          <div className="traffic-card rx"><span><ArrowDown size={13} /> RX</span><b>{formatBps(link.rx_bps)}</b></div>
        </div>
        <div className="util-bar"><span style={{ width: `${Math.min(link.utilization_pct, 100)}%` }} className={link.utilization_level} /></div>
        <div className="chart-head"><span>Tráfico · 24 h</span><span className="mono muted">{formatBps(link.max_speed_bps)}</span></div>
        <MetricChart data={metrics} kind="traffic" />

        <div className="section-label">Detalles</div>
        <KeyValue k="Capacidad" v={formatBps(link.max_speed_bps)} />
        <KeyValue k="Mastering" v={link.mastering_type === 'snmp' ? 'SNMP' : 'Simple'} />
        <KeyValue k="Interfaz" v={link.snmp_if_name || (link.snmp_if_index ? `ifIndex ${link.snmp_if_index}` : '—')} mono />
        <KeyValue k="Umbral aviso" v={`${link.warning_utilization_pct}%`} />
        <KeyValue k="Umbral crítico" v={`${link.critical_utilization_pct}%`} />
        <KeyValue k="Color" v={link.color_by_traffic === false ? 'Por estado' : 'Por tráfico'} />

        {editing ? (
          <div className="edit-box">
            <div className="inline-fields">
              <Field label="Tipo"><select className="input" value={linkType} onChange={e => setLinkType(e.target.value)}><option value="ethernet">Ethernet</option><option value="fiber">Fibra</option><option value="wireless">Inalámbrico</option><option value="vpn">VPN</option><option value="lag">LAG</option></select></Field>
              <Field label="Capacidad (Mbps)"><input className="input" type="number" value={maxSpeed} onChange={e => setMaxSpeed(Number(e.target.value))} /></Field>
            </div>
            <div className="inline-fields">
              <Field label="Aviso %"><input className="input" type="number" value={warnPct} onChange={e => setWarnPct(Number(e.target.value))} /></Field>
              <Field label="Crítico %"><input className="input" type="number" value={critPct} onChange={e => setCritPct(Number(e.target.value))} /></Field>
            </div>
            <Field label="Mastering"><select className="input" value={mastering} onChange={e => setMastering(e.target.value as any)}><option value="simple">Simple</option><option value="snmp">SNMP (interfaz)</option></select></Field>
            <label className="check-label"><input type="checkbox" checked={colorByTraffic} onChange={e => setColorByTraffic(e.target.checked)} /> Colorear por tráfico (usa Aviso % y Crítico %)</label>
            {mastering === 'snmp' && (
              <>
                <Field label="Dispositivo maestro">
                  <select className="input" value={masterDevice} onChange={e => setMasterDevice(e.target.value)}>
                    <option value="">— Selecciona</option>
                    {devices.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}
                  </select>
                </Field>
                {interfaces.length > 0 ? (
                  <Field label="Interfaz SNMP" hint={loadingIfaces ? 'Consultando ifTable…' : `${interfaces.length} interfaces detectadas`}>
                    <select
                      className="input"
                      value={ifIndex || ''}
                      onChange={e => {
                        const idx = Number(e.target.value)
                        const found = interfaces.find(i => i.if_index === idx)
                        setIfIndex(idx)
                        setIfName(found?.name || '')
                      }}
                    >
                      <option value="">— Selecciona interfaz</option>
                      {interfaces.map(i => (
                        <option key={i.if_index} value={i.if_index}>
                          {i.name} · ifIndex {i.if_index} · {i.oper_status === 1 ? 'up' : 'down'} · {i.speed_mbps ? `${i.speed_mbps} Mbps` : 'sin velocidad'}
                        </option>
                      ))}
                    </select>
                  </Field>
                ) : (
                  <div className="inline-fields">
                    <Field label="ifIndex"><input className="input" type="number" value={ifIndex} onChange={e => setIfIndex(Number(e.target.value))} /></Field>
                    <Field label="ifName"><input className="input" value={ifName} onChange={e => setIfName(e.target.value)} placeholder="sfp-sfpplus1" /></Field>
                  </div>
                )}
              </>
            )}
            <div className="inline-actions">
              <Button variant="ghost" onClick={() => setEditing(false)}>Cancelar</Button>
              <Button variant="primary" onClick={save} disabled={saving}>{saving ? 'Guardando…' : 'Guardar enlace'}</Button>
            </div>
          </div>
        ) : (
          <div className="inline-actions">
            <Button variant="secondary" onClick={() => setEditing(true)}><Pencil size={14} /> Editar enlace</Button>
            <Button variant="danger" size="sm" onClick={async () => { if (!confirm('¿Eliminar este enlace?')) return; await apiSend(`/maps/${mapId}/links/${link.id}`, 'DELETE'); onClose(); onRefresh() }}><Trash2 size={14} /></Button>
          </div>
        )}
      </div>
    </div>
  )
}

export default function Inspector({
  data,
  mapId,
  selectedNodeId,
  selectedEdgeId,
  onClose,
  onRefresh,
  onNavigate,
  refreshKey,
}: {
  data: CanvasData | null
  mapId: string
  selectedNodeId: string | null
  selectedEdgeId: string | null
  onClose: () => void
  onRefresh: () => void
  onNavigate: (id: string) => void
  refreshKey: number
}) {
  const element = useMemo(() => data?.elements.find(e => e.id === selectedNodeId) || null, [data, selectedNodeId])
  const link = useMemo(() => data?.links.find(e => e.id === selectedEdgeId) || null, [data, selectedEdgeId])

  if (!data) return null
  if (element) {
    if (element.element_type === 'zone' || element.element_type === 'text_label' || element.element_type === 'network') {
      return <aside className="inspector"><MiscElementInspector key={element.id} mapId={mapId} element={element} onRefresh={onRefresh} onClose={onClose} /></aside>
    }
    return <aside className="inspector"><ElementInspector key={element.id} mapId={mapId} element={element} onRefresh={onRefresh} onClose={onClose} onNavigate={onNavigate} refreshKey={refreshKey} /></aside>
  }
  if (link) {
    return <aside className="inspector"><EdgeInspector key={link.id} mapId={mapId} link={link} elements={data.elements} onRefresh={onRefresh} onClose={onClose} refreshKey={refreshKey} /></aside>
  }

  return (
    <aside className="inspector alone">
      <div className="inspector-inner">
        <div className="inspector-head">
          <div className="inspector-icon neutral"><Search size={19} /></div>
          <div className="inspector-title"><span className="eyebrow">Inspector</span><h2>Resumen del mapa</h2><div className="inspector-subline"><span>Selecciona un nodo o enlace</span></div></div>
        </div>
        <div className="inspector-body">
          <div className="inspector-summary-grid four">
            <div><b className="ok">{data.summary.up}</b><span>Operativos</span></div>
            <div><b className="warn">{data.summary.warning}</b><span>Avisos</span></div>
            <div><b className="danger">{data.summary.down}</b><span>Caídos</span></div>
            <div><b className="violet">{data.summary.unreachable}</b><span>Aislados</span></div>
          </div>
          <div className="inspector-map-desc">{data.map.description || 'Mapa visual de infraestructura.'}</div>
          <div className="section-label">Leyenda de estados</div>
          <div className="legend-list">
            <div><span className="legend-shape status-up" /> Operativo</div>
            <div><span className="legend-shape status-warning" /> Advertencia / reintento</div>
            <div><span className="legend-shape status-down" /> Caído</div>
            <div><span className="legend-shape status-unreachable" /> Inalcanzable por dependencia</div>
            <div><span className="legend-shape status-acknowledged" /> Reconocido</div>
            <div><span className="legend-shape status-disabled" /> Mantenimiento</div>
          </div>
          <div className="section-label">Atajos</div>
          <div className="shortcut-list">
            <div><kbd>Doble clic</kbd><span>Entrar en submapa</span></div>
            <div><kbd>Arrastrar</kbd><span>Mover nodo en modo edición</span></div>
            <div><kbd>Rueda</kbd><span>Zoom del lienzo</span></div>
            <div><kbd>Supr</kbd><span>Quitar elemento seleccionado</span></div>
          </div>
          <div className="inspector-tip"><Terminal size={15} /> El motor SNMP lee los OIDs cacheados y renderiza las etiquetas en cada ciclo de sondeo.</div>
        </div>
      </div>
    </aside>
  )
}
