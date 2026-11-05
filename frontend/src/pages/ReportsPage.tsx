import { useEffect, useMemo, useState } from 'react'
import {
  AlertTriangle,
  BarChart3,
  CalendarClock,
  CheckCircle2,
  Download,
  FileText,
  Gauge,
  HardDriveDownload,
  Plus,
  RefreshCw,
  Server,
  Trash2,
  Users,
} from 'lucide-react'
import { apiGet, apiSend, getToken, getUser } from '../api'
import { confirmAction } from '../confirm'
import type {
  MapInfo,
  ReportClient,
  ReportDevice,
  ReportInterface,
  ReportMonthly,
  ReportRun,
  ReportSchedule,
  ReportStorageConfig,
  ReportStorageResponse,
  ReportSummary,
  SLAClient,
} from '../types'
import { Button, EmptyState, Field, Modal, Spinner, StatusBadge, formatBps, formatDuration, timeAgo } from '../ui'

type Tab = 'overview' | 'devices' | 'clients' | 'interfaces' | 'monthly' | 'schedules' | 'storage'
type RangeKey = '24h' | '7d' | '30d' | '90d' | 'custom'

const EMPTY_SCHEDULE = {
  name: '', report_type: 'sla' as ReportSchedule['report_type'], format: 'pdf' as ReportSchedule['format'],
  frequency: 'weekly' as ReportSchedule['frequency'], hour_utc: 6, day_of_week: 1, day_of_month: 1,
  range_days: 30, map_id: '', client_id: '', include_interfaces: true, export_external: false, is_enabled: true,
}

const EMPTY_CLIENT = { name: '', description: '', map_ids: [] as string[], tags: [] as string[], target_pct: 99.5 }
const EMPTY_STORAGE: ReportStorageConfig = {
  type: 'local', endpoint: '', region: 'us-east-1', bucket: '', prefix: '', access_key: '', secret_key: '',
  path: '', public_base_url: '', path_style: true,
}

function rangeToDates(key: RangeKey, customFrom: string, customTo: string) {
  if (key === 'custom') {
    const fromDate = new Date(customFrom + 'T00:00:00')
    const toDate = new Date(customTo + 'T23:59:59')
    if (!Number.isNaN(fromDate.getTime()) && !Number.isNaN(toDate.getTime()) && toDate > fromDate) {
      return { from: fromDate.toISOString(), to: toDate.toISOString() }
    }
    const fallbackTo = new Date()
    return { from: new Date(fallbackTo.getTime() - 30 * 86400_000).toISOString(), to: fallbackTo.toISOString() }
  }
  const hours = key === '24h' ? 24 : key === '7d' ? 24 * 7 : key === '90d' ? 24 * 90 : 24 * 30
  const to = new Date()
  return { from: new Date(to.getTime() - hours * 3600_000).toISOString(), to: to.toISOString() }
}

function percentClass(value: number) {
  if (value >= 99.9) return 'ok'
  if (value >= 99) return 'warn'
  return 'danger'
}

function formatBytes(bytes: number) {
  if (!bytes) return '—'
  if (bytes > 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${Math.round(bytes / 1024)} KB`
}

function untilLabel(iso: string) {
  if (!iso) return '—'
  const diff = (new Date(iso).getTime() - Date.now()) / 1000
  if (diff <= 0) return 'pendiente'
  if (diff < 3600) return `en ${Math.ceil(diff / 60)}m`
  if (diff < 86400) return `en ${Math.ceil(diff / 3600)}h`
  return `en ${Math.ceil(diff / 86400)}d`
}

function safeExternalURL(raw: string) {
  if (!raw) return ''
  try {
    const parsed = new URL(raw)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? raw : ''
  } catch {
    return ''
  }
}

export default function ReportsPage() {
  const isAdmin = getUser()?.role === 'admin'
  const [tab, setTab] = useState<Tab>('overview')
  const [rangeKey, setRangeKey] = useState<RangeKey>('30d')
  const [customFrom, setCustomFrom] = useState(new Date(Date.now() - 30 * 86400_000).toISOString().slice(0, 10))
  const [customTo, setCustomTo] = useState(new Date().toISOString().slice(0, 10))
  const [mapID, setMapID] = useState('')
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [maps, setMaps] = useState<MapInfo[]>([])
  const [clients, setClients] = useState<SLAClient[]>([])
  const [summary, setSummary] = useState<ReportSummary | null>(null)
  const [devices, setDevices] = useState<ReportDevice[]>([])
  const [clientRows, setClientRows] = useState<ReportClient[]>([])
  const [interfaces, setInterfaces] = useState<ReportInterface[]>([])
  const [monthly, setMonthly] = useState<ReportMonthly[]>([])
  const [schedules, setSchedules] = useState<ReportSchedule[]>([])
  const [runs, setRuns] = useState<ReportRun[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const [clientOpen, setClientOpen] = useState(false)
  const [clientForm, setClientForm] = useState({ ...EMPTY_CLIENT })
  const [editingClient, setEditingClient] = useState<SLAClient | null>(null)
  const [scheduleOpen, setScheduleOpen] = useState(false)
  const [scheduleForm, setScheduleForm] = useState({ ...EMPTY_SCHEDULE })
  const [editingSchedule, setEditingSchedule] = useState<ReportSchedule | null>(null)
  const [generateOpen, setGenerateOpen] = useState(false)
  const [generateForm, setGenerateForm] = useState({ report_type: 'sla', format: 'pdf', include_interfaces: true, export_external: false })
  const [storage, setStorage] = useState<ReportStorageConfig>({ ...EMPTY_STORAGE })
  const [hasSecret, setHasSecret] = useState(false)

  const dates = useMemo(() => rangeToDates(rangeKey, customFrom, customTo), [rangeKey, customFrom, customTo, refreshNonce])
  const query = useMemo(() => {
    const params = new URLSearchParams({ from: dates.from, to: dates.to })
    if (mapID) params.set('map_id', mapID)
    return params.toString()
  }, [dates, mapID])

  const flash = (msg: string) => { setNotice(msg); window.setTimeout(() => setNotice(''), 5000) }

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      if (tab === 'overview') {
        const [s, m] = await Promise.all([apiGet<ReportSummary>(`/reports/overview?${query}`), apiGet<MapInfo[]>('/maps')])
        setSummary(s); setMaps(m)
      } else if (tab === 'devices') {
        const [d, m] = await Promise.all([apiGet<{ devices: ReportDevice[] }>(`/reports/devices?${query}`), apiGet<MapInfo[]>('/maps')])
        setDevices(d.devices || []); setMaps(m)
      } else if (tab === 'clients') {
        const [c, sc, m] = await Promise.all([
          apiGet<{ clients: ReportClient[] }>(`/reports/clients?${query}`),
          apiGet<SLAClient[]>('/reports/sla-clients'),
          apiGet<MapInfo[]>('/maps'),
        ])
        setClientRows(c.clients || []); setClients(sc); setMaps(m)
      } else if (tab === 'interfaces') {
        const [i, m] = await Promise.all([apiGet<{ interfaces: ReportInterface[] }>(`/reports/interfaces?${query}`), apiGet<MapInfo[]>('/maps')])
        setInterfaces(i.interfaces || []); setMaps(m)
      } else if (tab === 'monthly') {
        const [m, ml] = await Promise.all([apiGet<ReportMonthly[]>('/reports/monthly?months=6' + (mapID ? `&map_id=${mapID}` : '')), apiGet<MapInfo[]>('/maps')])
        setMonthly(m); setMaps(ml)
      } else if (tab === 'schedules') {
        const [s, r, m, c] = await Promise.all([
          apiGet<ReportSchedule[]>('/reports/schedules'),
          apiGet<ReportRun[]>('/reports/runs'),
          apiGet<MapInfo[]>('/maps'),
          apiGet<SLAClient[]>('/reports/sla-clients').catch(() => []),
        ])
        setSchedules(s); setRuns(r); setMaps(m); setClients(c)
      } else if (tab === 'storage') {
        const data = await apiGet<ReportStorageResponse>('/reports/storage')
        setStorage({ ...EMPTY_STORAGE, ...data.config, secret_key: '' })
        setHasSecret(data.has_secret)
      }
    } catch (e) { setError((e as Error).message) } finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [tab, query])

  const openClient = (client?: SLAClient) => {
    setEditingClient(client || null)
    setClientForm(client ? { name: client.name, description: client.description, map_ids: client.map_ids || [], tags: client.tags || [], target_pct: client.target_pct } : { ...EMPTY_CLIENT })
    setClientOpen(true)
  }

  const saveClient = async () => {
    setBusy('client'); setError('')
    try {
      if (!clientForm.name.trim()) throw new Error('El nombre es obligatorio')
      if (editingClient) await apiSend(`/reports/sla-clients/${editingClient.id}`, 'PUT', clientForm)
      else await apiSend('/reports/sla-clients', 'POST', clientForm)
      setClientOpen(false)
      await load()
      flash('Cliente SLA guardado')
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  const deleteClient = async (client: SLAClient) => {
    if (!await confirmAction(`¿Eliminar el cliente SLA "${client.name}"?`)) return
    try { await apiSend(`/reports/sla-clients/${client.id}`, 'DELETE'); await load() } catch (e) { setError((e as Error).message) }
  }

  const openSchedule = (schedule?: ReportSchedule) => {
    setEditingSchedule(schedule || null)
    setScheduleForm(schedule ? {
      name: schedule.name, report_type: schedule.report_type, format: schedule.format, frequency: schedule.frequency,
      hour_utc: schedule.hour_utc, day_of_week: schedule.day_of_week, day_of_month: schedule.day_of_month,
      range_days: schedule.range_days, map_id: schedule.map_id || '', client_id: schedule.client_id || '',
      include_interfaces: schedule.include_interfaces, export_external: schedule.export_external, is_enabled: schedule.is_enabled,
    } : { ...EMPTY_SCHEDULE })
    setScheduleOpen(true)
  }

  const saveSchedule = async () => {
    setBusy('schedule'); setError('')
    try {
      if (!scheduleForm.name.trim()) throw new Error('El nombre es obligatorio')
      if (editingSchedule) await apiSend(`/reports/schedules/${editingSchedule.id}`, 'PUT', scheduleForm)
      else await apiSend('/reports/schedules', 'POST', scheduleForm)
      setScheduleOpen(false)
      await load()
      flash('Programación guardada')
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  const deleteSchedule = async (schedule: ReportSchedule) => {
    if (!await confirmAction(`¿Eliminar la programación "${schedule.name}"?`)) return
    try { await apiSend(`/reports/schedules/${schedule.id}`, 'DELETE'); await load() } catch (e) { setError((e as Error).message) }
  }

  const runSchedule = async (schedule: ReportSchedule) => {
    setBusy('run-' + schedule.id); setError('')
    try {
      const run = await apiSend<ReportRun>(`/reports/schedules/${schedule.id}/run`, 'POST')
      await load()
      await downloadRun(run)
      flash('Informe generado')
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  const generateNow = async () => {
    setBusy('generate'); setError('')
    try {
      const run = await apiSend<ReportRun>('/reports/generate', 'POST', {
        report_type: generateForm.report_type, format: generateForm.format,
        from: dates.from, to: dates.to, map_id: mapID,
        include_interfaces: generateForm.include_interfaces, export_external: generateForm.export_external,
      })
      setGenerateOpen(false)
      await downloadRun(run)
      if (tab === 'schedules') await load()
      flash('Informe generado')
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  const downloadRun = async (run: ReportRun) => {
    const token = getToken()
    const res = await fetch(`/api/v1/reports/runs/${run.id}/download`, { headers: token ? { Authorization: `Bearer ${token}` } : {} })
    if (!res.ok) {
      const data = await res.json().catch(() => null)
      throw new Error(data?.error || `Error HTTP ${res.status}`)
    }
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = run.filename || 'informe'
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
  }

  const saveStorage = async () => {
    setBusy('storage'); setError('')
    try {
      await apiSend('/reports/storage', 'PUT', storage)
      await load()
      flash('Almacenamiento guardado')
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  const testStorage = async () => {
    setBusy('storage-test'); setError('')
    try {
      const data = await apiSend<{ url: string }>('/reports/storage/test', 'POST', storage)
      flash('Exportación correcta: ' + data.url)
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  const exportCSV = async (type: string) => {
    setBusy('export')
    setError('')
    try {
      const run = await apiSend<ReportRun>('/reports/generate', 'POST', {
        report_type: type, format: 'csv', from: dates.from, to: dates.to, map_id: mapID,
      })
      await downloadRun(run)
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Fase 3 · Analítica</span>
          <h1>Informes avanzados</h1>
          <p className="page-subtitle">Disponibilidad, SLA por cliente, interfaces, comparativas mensuales y PDF programado.</p>
        </div>
        <div className="page-head-actions">
          <Button variant="secondary" onClick={() => setRefreshNonce(n => n + 1)}><RefreshCw size={14} /> Actualizar</Button>
          <Button variant="secondary" onClick={() => setGenerateOpen(true)}><FileText size={14} /> Generar PDF</Button>
          <Button variant="primary" onClick={() => void exportCSV(tab === 'interfaces' ? 'interfaces' : tab === 'clients' ? 'clients' : tab === 'monthly' ? 'executive' : 'sla')} disabled={busy === 'export'}><Download size={14} /> CSV</Button>
        </div>
      </div>

      <div className="page-tabs">
        <button className={tab === 'overview' ? 'active' : ''} onClick={() => setTab('overview')}><Gauge size={15} /> Resumen</button>
        <button className={tab === 'devices' ? 'active' : ''} onClick={() => setTab('devices')}><Server size={15} /> Dispositivos</button>
        <button className={tab === 'clients' ? 'active' : ''} onClick={() => setTab('clients')}><Users size={15} /> Clientes SLA</button>
        <button className={tab === 'interfaces' ? 'active' : ''} onClick={() => setTab('interfaces')}><BarChart3 size={15} /> Interfaces</button>
        <button className={tab === 'monthly' ? 'active' : ''} onClick={() => setTab('monthly')}><CalendarClock size={15} /> Mensual</button>
        {isAdmin && <button className={tab === 'schedules' ? 'active' : ''} onClick={() => setTab('schedules')}><FileText size={15} /> Programados</button>}
        {isAdmin && <button className={tab === 'storage' ? 'active' : ''} onClick={() => setTab('storage')}><HardDriveDownload size={15} /> Almacenamiento</button>}
      </div>

      <div className="report-toolbar table-card">
        <div className="filter-chips">
          {(['24h', '7d', '30d', '90d', 'custom'] as RangeKey[]).map(r => <button key={r} className={`chip ${rangeKey === r ? 'active' : ''}`} onClick={() => setRangeKey(r)}>{r === 'custom' ? 'Personalizado' : r}</button>)}
        </div>
        {rangeKey === 'custom' && <div className="report-dates"><input className="input" type="date" value={customFrom} onChange={e => setCustomFrom(e.target.value)} /><span>→</span><input className="input" type="date" value={customTo} onChange={e => setCustomTo(e.target.value)} /></div>}
        <select className="input" style={{ maxWidth: 230 }} value={mapID} onChange={e => setMapID(e.target.value)}><option value="">Todos los mapas</option>{maps.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}</select>
      </div>

      {notice && <div className="notice-ok"><CheckCircle2 size={15} /> {notice}</div>}
      {error && <div className="form-error"><AlertTriangle size={14} /> {error}</div>}

      {loading ? <div className="page-center"><Spinner /></div> : <>
        {tab === 'overview' && summary && (
          <>
            <div className="report-cards">
              <div className="report-card"><span>Disponibilidad global</span><b className={`report-big ${percentClass(summary.availability_pct)}`}>{summary.availability_pct.toFixed(3)}<small>%</small></b><div className="report-progress"><span className={percentClass(summary.availability_pct)} style={{ width: `${summary.availability_pct}%` }} /></div></div>
              <div className="report-card"><span>Incidencias</span><b className="report-big">{summary.incidents}</b><small>{summary.active_outages} activas ahora</small></div>
              <div className="report-card"><span>Tiempo de caída</span><b className="report-big">{formatDuration(summary.downtime_sec)}</b><small>MTTR {formatDuration(summary.mttr_sec)}</small></div>
              <div className="report-card"><span>Equipos</span><b className="report-big">{summary.devices}</b><small>{summary.up} up · {summary.warning} aviso · {summary.down} down · {summary.unreachable} aislados</small></div>
            </div>
            <div className="table-card">
              <div className="table-toolbar"><b>Dispositivos con más indisponibilidad</b><span className="muted small">Periodo de {new Date(summary.from).toLocaleDateString('es-ES')} a {new Date(summary.to).toLocaleDateString('es-ES')}</span></div>
              <div className="data-table report-devices-table">
                <div className="data-row head"><span>Dispositivo</span><span>Incidencias</span><span>Caída</span><span>Disponibilidad</span><span>Causa raíz</span></div>
                {summary.top_down.map(d => (
                  <div className="data-row" key={d.device_id}>
                    <span className="cell-main"><b>{d.device_name}</b><small className="mono">{d.primary_ip}</small></span>
                    <span className="mono">{d.incidents}</span>
                    <span className="mono">{formatDuration(d.downtime_sec)}</span>
                    <span className={`mono ${percentClass(d.availability_pct)}`}>{d.availability_pct.toFixed(3)} %</span>
                    <span className="muted small">{d.root_cause || '—'}</span>
                  </div>
                ))}
                {!summary.top_down.length && <EmptyState title="Sin incidencias" message="No hay caídas en el periodo seleccionado." icon={<CheckCircle2 size={24} />} />}
              </div>
            </div>
          </>
        )}

        {tab === 'devices' && (
          <div className="table-card">
            <div className="data-table report-devices-table">
              <div className="data-row head"><span>Dispositivo</span><span>Estado</span><span>Incidencias</span><span>Caída</span><span>Disponibilidad</span><span>Causa raíz</span></div>
              {devices.map(d => (
                <div className="data-row" key={d.device_id}>
                  <span className="cell-main"><b>{d.device_name}</b><small className="mono">{d.primary_ip} · {d.tags.join(', ') || d.device_type}</small></span>
                  <span><StatusBadge status={d.status} /></span>
                  <span className="mono">{d.incidents}</span>
                  <span className="mono">{formatDuration(d.downtime_sec)}</span>
                  <span className={`mono ${percentClass(d.availability_pct)}`}>{d.availability_pct.toFixed(3)} %</span>
                  <span className="muted small">{d.root_cause || '—'}</span>
                </div>
              ))}
              {!devices.length && <EmptyState title="Sin dispositivos" message="Selecciona otro mapa o periodo." icon={<Server size={24} />} />}
            </div>
          </div>
        )}

        {tab === 'clients' && (
          <>
            <div className="table-card">
              <div className="table-toolbar"><b>Clientes SLA</b><Button size="sm" variant="primary" onClick={() => openClient()}><Plus size={13} /> Nuevo cliente</Button></div>
              <div className="data-table report-clients-table">
                <div className="data-row head"><span>Cliente</span><span>Equipos</span><span>Incidencias</span><span>Disponibilidad</span><span>Objetivo</span><span>Estado</span><span /></div>
                {clientRows.map(c => (
                  <div className="data-row" key={c.id}>
                    <span className="cell-main"><b>{c.name}</b><small>{c.description || 'Sin descripción'}</small></span>
                    <span className="mono">{c.devices}</span>
                    <span className="mono">{c.incidents}</span>
                    <span className={`mono ${percentClass(c.availability_pct)}`}>{c.availability_pct.toFixed(3)} %</span>
                    <span className="mono">{c.target_pct.toFixed(2)} %</span>
                    <span><StatusBadge status={c.compliant ? 'up' : 'down'} text={c.compliant ? 'Cumple' : 'No cumple'} /></span>
                    <span className="row-actions">{clients.filter(x => x.id === c.id).map(x => <span key={x.id} className="row-actions"><Button size="sm" variant="ghost" onClick={() => openClient(x)}>Editar</Button><Button size="sm" variant="ghost" onClick={() => void deleteClient(x)}><Trash2 size={13} /></Button></span>)}</span>
                  </div>
                ))}
                {!clientRows.length && <EmptyState title="Sin clientes SLA" message="Crea un cliente con filtro por mapa o etiqueta para medir su disponibilidad." icon={<Users size={24} />} />}
              </div>
            </div>
            <div className="table-card">
              <div className="table-toolbar"><b>Definiciones de clientes</b><span className="muted small">Los filtros vacíos incluyen todos los equipos.</span></div>
              <div className="data-table report-clients-table">
                <div className="data-row head"><span>Cliente</span><span>Mapas</span><span>Etiquetas</span><span>Objetivo</span><span /></div>
                {clients.map(c => <div className="data-row" key={c.id}><span className="cell-main"><b>{c.name}</b><small>{c.description}</small></span><span className="muted small">{c.map_ids.length ? maps.filter(m => c.map_ids.includes(m.id)).map(m => m.name).join(', ') : 'Todos'}</span><span className="muted small">{c.tags.join(', ') || 'Todas'}</span><span className="mono">{c.target_pct.toFixed(2)} %</span><span className="row-actions"><Button size="sm" variant="ghost" onClick={() => openClient(c)}>Editar</Button><Button size="sm" variant="ghost" onClick={() => void deleteClient(c)}><Trash2 size={13} /></Button></span></div>)}
              </div>
            </div>
          </>
        )}

        {tab === 'interfaces' && (
          <div className="table-card">
            <div className="data-table report-interfaces-table">
              <div className="data-row head"><span>Mapa</span><span>Interfaz</span><span>Tipo</span><span>Muestras</span><span>Disponibilidad</span><span>RX medio</span><span>TX medio</span><span>Errores</span></div>
              {interfaces.map(i => (
                <div className="data-row" key={i.link_id}>
                  <span className="cell-main"><b>{i.map_name}</b><small>{i.device_name || 'Sin maestro'}</small></span>
                  <span className="mono">{i.interface_name}</span>
                  <span>{i.link_type}</span>
                  <span className="mono small">{i.samples}</span>
                  <span className={`mono ${i.availability_pct < 0 ? 'muted' : percentClass(i.availability_pct)}`}>{i.availability_pct < 0 ? 'sin datos' : `${i.availability_pct.toFixed(2)} %`}</span>
                  <span className="mono">{formatBps(i.avg_rx_bps)}</span>
                  <span className="mono">{formatBps(i.avg_tx_bps)}</span>
                  <span className="mono">{i.max_errors}</span>
                </div>
              ))}
              {!interfaces.length && <EmptyState title="Sin interfaces" message="Los enlaces con mastering SNMP alimentan esta vista con link_metrics." icon={<BarChart3 size={24} />} />}
            </div>
          </div>
        )}

        {tab === 'monthly' && (
          <div className="table-card">
            <div className="table-toolbar"><b>Comparativa mensual</b><span className="muted small">Δ frente al mes anterior.</span></div>
            <div className="monthly-chart">
              {monthly.map(m => <div className="monthly-bar" key={m.month}><div className="monthly-bar-track"><span style={{ height: `${Math.max(2, m.availability_pct)}%` }} className={percentClass(m.availability_pct)} /></div><b>{m.month}</b><small>{m.availability_pct.toFixed(2)}%</small></div>)}
            </div>
            <div className="data-table report-monthly-table">
              <div className="data-row head"><span>Mes</span><span>Equipos</span><span>Incidencias</span><span>Caída</span><span>Disponibilidad</span><span>Δ disponibilidad</span><span>Δ incidencias</span></div>
              {monthly.map(m => <div className="data-row" key={m.month}><span className="mono">{m.month}</span><span className="mono">{m.devices}</span><span className="mono">{m.incidents}</span><span className="mono">{formatDuration(m.downtime_sec)}</span><span className={`mono ${percentClass(m.availability_pct)}`}>{m.availability_pct.toFixed(3)} %</span><span className={`mono ${m.delta_pct >= 0 ? 'ok' : 'danger'}`}>{m.delta_pct >= 0 ? '+' : ''}{m.delta_pct.toFixed(3)} %</span><span className={`mono ${m.delta_incidents <= 0 ? 'ok' : 'danger'}`}>{m.delta_incidents > 0 ? '+' : ''}{m.delta_incidents}</span></div>)}
            </div>
          </div>
        )}

        {tab === 'schedules' && (
          <>
            <div className="table-card">
              <div className="table-toolbar"><b>Informes programados</b><Button size="sm" variant="primary" onClick={() => openSchedule()}><Plus size={13} /> Nueva programación</Button></div>
              <div className="data-table report-schedules-table">
                <div className="data-row head"><span>Nombre</span><span>Tipo / formato</span><span>Frecuencia</span><span>Próxima</span><span>Exportación</span><span /></div>
                {schedules.map(s => (
                  <div className="data-row" key={s.id}>
                    <span className="cell-main"><b>{s.name}</b><small>{s.is_enabled ? 'Activa' : 'Pausada'} · {s.range_days} días</small></span>
                    <span>{s.report_type} · {s.format.toUpperCase()}</span>
                    <span>{s.frequency === 'daily' ? `Diaria ${s.hour_utc}:00 UTC` : s.frequency === 'weekly' ? `Semanal (día ${s.day_of_week})` : `Mensual (día ${s.day_of_month})`}</span>
                    <span className="muted small">{untilLabel(s.next_run_at)}</span>
                    <span className="muted small">{s.export_external ? 'Sí' : 'Solo local'}</span>
                    <span className="row-actions"><Button size="sm" variant="ghost" onClick={() => void runSchedule(s)} disabled={busy === 'run-' + s.id}><Download size={13} /> Ejecutar</Button><Button size="sm" variant="ghost" onClick={() => openSchedule(s)}>Editar</Button><Button size="sm" variant="ghost" onClick={() => void deleteSchedule(s)}><Trash2 size={13} /></Button></span>
                  </div>
                ))}
                {!schedules.length && <EmptyState title="Sin informes programados" message="Programa un PDF diario, semanal o mensual y envíalo opcionalmente a S3." icon={<CalendarClock size={24} />} />}
              </div>
            </div>
            <div className="table-card">
              <div className="table-toolbar"><b>Ejecuciones recientes</b><span className="muted small">Descarga local o URL externa cuando la exportación está activa.</span></div>
              <div className="data-table report-runs-table">
                <div className="data-row head"><span>Informe</span><span>Periodo</span><span>Estado</span><span>Tamaño</span><span>Generado</span><span /></div>
                {runs.map(r => (
                  <div className="data-row" key={r.id}>
                    <span className="cell-main"><b>{r.filename || r.report_type}</b><small>{r.generated_by}</small></span>
                    <span className="muted small">{new Date(r.period_from).toLocaleDateString('es-ES')} → {new Date(r.period_to).toLocaleDateString('es-ES')}</span>
                    <span><StatusBadge status={r.status === 'completed' ? 'up' : r.status === 'failed' ? 'down' : 'warning'} text={r.status} /></span>
                    <span className="mono small">{formatBytes(r.size_bytes)}</span>
                    <span className="muted small">{timeAgo(r.created_at)}</span>
                    <span className="row-actions">{r.status === 'completed' && <Button size="sm" variant="ghost" onClick={() => void downloadRun(r)}><Download size={13} /> Descargar</Button>}{safeExternalURL(r.external_url) && <a className="btn btn-ghost btn-sm" href={safeExternalURL(r.external_url)} target="_blank" rel="noreferrer">Externo</a>}</span>
                  </div>
                ))}
                {!runs.length && <EmptyState title="Sin ejecuciones" message="Los informes generados aparecerán aquí para descarga." icon={<FileText size={24} />} />}
              </div>
            </div>
          </>
        )}

        {tab === 'storage' && (
          <div className="table-card">
            <div className="settings-block">
              <div><h3>Almacenamiento externo</h3><p className="muted small">Copia los informes generados a un directorio local o a S3/MinIO con Signature V4. Las credenciales se cifran en reposo.</p></div>
              <div className="form-grid two">
                <Field label="Tipo"><select className="input" value={storage.type} onChange={e => setStorage({ ...storage, type: e.target.value })}><option value="local">Directorio local</option><option value="s3">S3 / MinIO</option></select></Field>
                <Field label="Ruta local" hint="Solo para tipo local. Puede ser un volumen montado."><input className="input mono" value={storage.path} onChange={e => setStorage({ ...storage, path: e.target.value })} /></Field>
                {storage.type === 's3' && <>
                  <Field label="Endpoint"><input className="input mono" value={storage.endpoint} onChange={e => setStorage({ ...storage, endpoint: e.target.value })} placeholder="https://s3.eu-west-1.amazonaws.com" /></Field>
                  <Field label="Región"><input className="input mono" value={storage.region} onChange={e => setStorage({ ...storage, region: e.target.value })} /></Field>
                  <Field label="Bucket"><input className="input mono" value={storage.bucket} onChange={e => setStorage({ ...storage, bucket: e.target.value })} /></Field>
                  <Field label="Prefijo"><input className="input mono" value={storage.prefix} onChange={e => setStorage({ ...storage, prefix: e.target.value })} placeholder="netmonitor/reports" /></Field>
                  <Field label="Access key"><input className="input mono" value={storage.access_key} onChange={e => setStorage({ ...storage, access_key: e.target.value })} /></Field>
                  <Field label={hasSecret ? 'Secret key (vacío = mantener)' : 'Secret key'}><input className="input" type="password" value={storage.secret_key} onChange={e => setStorage({ ...storage, secret_key: e.target.value })} /></Field>
                  <Field label="URL pública (opcional)"><input className="input mono" value={storage.public_base_url} onChange={e => setStorage({ ...storage, public_base_url: e.target.value })} /></Field>
                  <label className="check-row" style={{ alignItems: 'center' }}><input type="checkbox" checked={storage.path_style} onChange={e => setStorage({ ...storage, path_style: e.target.checked })} /> Path-style (MinIO/Ceph)</label>
                </>}
              </div>
              <div className="row-actions"><Button variant="secondary" onClick={() => void testStorage()} disabled={busy === 'storage-test'}><HardDriveDownload size={14} /> Probar exportación</Button><Button variant="primary" onClick={() => void saveStorage()} disabled={busy === 'storage'}>Guardar almacenamiento</Button></div>
            </div>
          </div>
        )}
      </>}

      <Modal open={clientOpen} onClose={() => setClientOpen(false)} title={editingClient ? 'Editar cliente SLA' : 'Nuevo cliente SLA'} subtitle="La disponibilidad se calcula con las incidencias de los equipos filtrados." width={640} footer={<><Button variant="ghost" onClick={() => setClientOpen(false)}>Cancelar</Button><Button variant="primary" onClick={() => void saveClient()} disabled={busy === 'client'}>{busy === 'client' ? 'Guardando…' : 'Guardar'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={clientForm.name} onChange={e => setClientForm({ ...clientForm, name: e.target.value })} /></Field>
          <Field label="Objetivo SLA (%)"><input className="input mono" type="number" step="0.01" value={clientForm.target_pct} onChange={e => setClientForm({ ...clientForm, target_pct: Number(e.target.value) })} /></Field>
        </div>
        <Field label="Descripción"><input className="input" value={clientForm.description} onChange={e => setClientForm({ ...clientForm, description: e.target.value })} /></Field>
        <Field label="Etiquetas incluidas" hint="Separadas por coma. Vacío = todas."><input className="input" value={clientForm.tags.join(', ')} onChange={e => setClientForm({ ...clientForm, tags: e.target.value.split(',').map(x => x.trim()).filter(Boolean) })} /></Field>
        <Field label="Mapas incluidos" hint="Vacío = todos los mapas.">
          <div className="target-picker compact">{maps.map(m => <label key={m.id} className={clientForm.map_ids.includes(m.id) ? 'selected' : ''}><input type="checkbox" checked={clientForm.map_ids.includes(m.id)} onChange={e => setClientForm({ ...clientForm, map_ids: e.target.checked ? [...clientForm.map_ids, m.id] : clientForm.map_ids.filter(x => x !== m.id) })} /><span><b>{m.name}</b><small>{m.counts.up + m.counts.down + (m.counts.acknowledged || 0) + m.counts.warning + m.counts.unreachable} equipos</small></span></label>)}</div>
        </Field>
      </Modal>

      <Modal open={scheduleOpen} onClose={() => setScheduleOpen(false)} title={editingSchedule ? 'Editar programación' : 'Nueva programación'} width={720} footer={<><Button variant="ghost" onClick={() => setScheduleOpen(false)}>Cancelar</Button><Button variant="primary" onClick={() => void saveSchedule()} disabled={busy === 'schedule'}>{busy === 'schedule' ? 'Guardando…' : 'Guardar'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={scheduleForm.name} onChange={e => setScheduleForm({ ...scheduleForm, name: e.target.value })} /></Field>
          <Field label="Tipo"><select className="input" value={scheduleForm.report_type} onChange={e => setScheduleForm({ ...scheduleForm, report_type: e.target.value as ReportSchedule['report_type'] })}><option value="sla">SLA y disponibilidad</option><option value="executive">Ejecutivo</option><option value="clients">Clientes SLA</option><option value="interfaces">Interfaces</option></select></Field>
          <Field label="Formato"><select className="input" value={scheduleForm.format} onChange={e => setScheduleForm({ ...scheduleForm, format: e.target.value as ReportSchedule['format'] })}><option value="pdf">PDF</option><option value="csv">CSV</option></select></Field>
          <Field label="Frecuencia"><select className="input" value={scheduleForm.frequency} onChange={e => setScheduleForm({ ...scheduleForm, frequency: e.target.value as ReportSchedule['frequency'] })}><option value="daily">Diaria</option><option value="weekly">Semanal</option><option value="monthly">Mensual</option></select></Field>
          <Field label="Hora UTC"><input className="input mono" type="number" min={0} max={23} value={scheduleForm.hour_utc} onChange={e => setScheduleForm({ ...scheduleForm, hour_utc: Number(e.target.value) })} /></Field>
          <Field label="Días del informe"><input className="input mono" type="number" min={1} max={3650} value={scheduleForm.range_days} onChange={e => setScheduleForm({ ...scheduleForm, range_days: Number(e.target.value) })} /></Field>
          {scheduleForm.frequency === 'weekly' && <Field label="Día de la semana" hint="0 domingo … 6 sábado."><input className="input mono" type="number" min={0} max={6} value={scheduleForm.day_of_week} onChange={e => setScheduleForm({ ...scheduleForm, day_of_week: Number(e.target.value) })} /></Field>}
          {scheduleForm.frequency === 'monthly' && <Field label="Día del mes"><input className="input mono" type="number" min={1} max={28} value={scheduleForm.day_of_month} onChange={e => setScheduleForm({ ...scheduleForm, day_of_month: Number(e.target.value) })} /></Field>}
          <Field label="Mapa"><select className="input" value={scheduleForm.map_id} onChange={e => setScheduleForm({ ...scheduleForm, map_id: e.target.value })}><option value="">Todos</option>{maps.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}</select></Field>
          {scheduleForm.report_type === 'clients' && <Field label="Cliente"><select className="input" value={scheduleForm.client_id} onChange={e => setScheduleForm({ ...scheduleForm, client_id: e.target.value })}><option value="">Todos</option>{clients.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></Field>}
        </div>
        <div className="check-row"><label><input type="checkbox" checked={scheduleForm.include_interfaces} onChange={e => setScheduleForm({ ...scheduleForm, include_interfaces: e.target.checked })} /> Incluir interfaces</label><label><input type="checkbox" checked={scheduleForm.export_external} onChange={e => setScheduleForm({ ...scheduleForm, export_external: e.target.checked })} /> Exportar a almacenamiento externo</label><label><input type="checkbox" checked={scheduleForm.is_enabled} onChange={e => setScheduleForm({ ...scheduleForm, is_enabled: e.target.checked })} /> Activa</label></div>
      </Modal>

      <Modal open={generateOpen} onClose={() => setGenerateOpen(false)} title="Generar informe ahora" subtitle="Se genera en el servidor y se descarga al terminar." width={560} footer={<><Button variant="ghost" onClick={() => setGenerateOpen(false)}>Cancelar</Button><Button variant="primary" onClick={() => void generateNow()} disabled={busy === 'generate'}>{busy === 'generate' ? 'Generando…' : 'Generar y descargar'}</Button></>}>
        <Field label="Tipo"><select className="input" value={generateForm.report_type} onChange={e => setGenerateForm({ ...generateForm, report_type: e.target.value })}><option value="sla">SLA y disponibilidad</option><option value="executive">Ejecutivo</option><option value="clients">Clientes SLA</option><option value="interfaces">Interfaces</option></select></Field>
        <div className="form-grid two"><Field label="Formato"><select className="input" value={generateForm.format} onChange={e => setGenerateForm({ ...generateForm, format: e.target.value })}><option value="pdf">PDF</option><option value="csv">CSV</option></select></Field></div>
        <div className="check-row"><label><input type="checkbox" checked={generateForm.include_interfaces} onChange={e => setGenerateForm({ ...generateForm, include_interfaces: e.target.checked })} /> Incluir interfaces</label><label><input type="checkbox" checked={generateForm.export_external} onChange={e => setGenerateForm({ ...generateForm, export_external: e.target.checked })} /> Exportar a almacenamiento externo</label></div>
      </Modal>
    </div>
  )
}
