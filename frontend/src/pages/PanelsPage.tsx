import { useEffect, useMemo, useState } from 'react'
import {
  BarChart3,
  Check,
  Expand,
  LayoutDashboard,
  List,
  Map as MapIcon,
  Maximize2,
  Minimize2,
  Pencil,
  Plus,
  RefreshCcw,
  Type,
  Trash2,
} from 'lucide-react'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { apiGet, apiSend, getUser } from '../api'
import { confirmAction } from '../confirm'
import type { CanvasData, DashboardPanel, DashboardSummary, MapInfo, MetricSeries, PanelWidget } from '../types'
import { Button, EmptyState, Field, Modal, Spinner, formatBps, statusClass } from '../ui'

const COLORS = ['#2563eb', '#0891b2', '#7c3aed', '#16a34a', '#f59e0b', '#dc2626', '#0f766e', '#db2777']
const RANGES = ['1h', '6h', '24h', '7d', '30d']

function newID() {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `widget-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function newWidget(kind: PanelWidget['kind']): PanelWidget {
  const base: PanelWidget = { id: newID(), kind, title: kind === 'chart' ? 'Nueva gráfica' : kind === 'table' ? 'Nueva tabla' : kind === 'map' ? 'Mapa' : kind === 'counter' ? 'Contador' : 'Texto' }
  if (kind === 'chart') { base.series = []; base.range = '24h' }
  if (kind === 'table') { base.source = 'outages'; base.limit = 8 }
  if (kind === 'counter') base.metric = 'up'
  if (kind === 'text') base.text = 'Texto del panel'
  return base
}

function ChartWidget({ widget, refreshKey }: { widget: PanelWidget; refreshKey: number }) {
  const [series, setSeries] = useState<MetricSeries[]>([])
  const [loading, setLoading] = useState(false)
  useEffect(() => {
    if (!widget.series?.length) { setSeries([]); return }
    setLoading(true)
    apiGet<any>(`/metrics/compare?series=${encodeURIComponent(widget.series.join(','))}&range=${widget.range || '24h'}`)
      .then(data => setSeries(data.series || []))
      .catch(() => setSeries([]))
      .finally(() => setLoading(false))
  }, [widget.series?.join(','), widget.range, refreshKey])
  const chart = useMemo(() => {
    const byTime = new Map<string, any>()
    series.forEach((s, i) => {
      s.points.forEach(p => {
        const key = p.time
        const row = byTime.get(key) || { time: key }
        row[`s${i}`] = p.value
        byTime.set(key, row)
      })
    })
    return Array.from(byTime.values()).sort((a, b) => a.time.localeCompare(b.time))
  }, [series])
  const isBps = series.some(s => s.unit === 'bps')
  if (!widget.series?.length) return <div className="widget-empty">Selecciona una o varias series al editar el panel.</div>
  if (loading && !series.length) return <div className="widget-empty"><Spinner label="Cargando serie" /></div>
  if (!chart.length) return <div className="widget-empty">Sin datos en el rango seleccionado.</div>
  return (
    <div className="widget-chart">
      <ResponsiveContainer width="100%" height={210}>
        <LineChart data={chart} margin={{ top: 6, right: 12, left: 0, bottom: 0 }}>
          <CartesianGrid stroke="#eef2f7" vertical={false} />
          <XAxis dataKey="time" hide />
          <YAxis tick={{ fontSize: 9, fill: '#94a3b8' }} axisLine={false} tickLine={false} width={isBps ? 60 : 42} tickFormatter={v => isBps ? formatBps(Number(v)) : Number(v).toFixed(0)} />
          <Tooltip contentStyle={{ borderRadius: 10, border: '1px solid #e2e8f0', fontSize: 11 }} labelFormatter={v => new Date(v).toLocaleString('es-ES')} formatter={(value: any, name: any) => {
            const idx = Number(String(name).replace('s', ''))
            return [isBps ? formatBps(Number(value)) : Number(value).toFixed(2), series[idx]?.label || name]
          }} />
          <Legend wrapperStyle={{ fontSize: 10 }} formatter={(value: any) => {
            const idx = Number(String(value).replace('s', ''))
            return series[idx]?.label || value
          }} />
          {series.map((s, i) => <Line key={s.id} type="monotone" dataKey={`s${i}`} stroke={COLORS[i % COLORS.length]} dot={false} strokeWidth={1.8} connectNulls />)}
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}

function TableWidget({ widget, refreshKey }: { widget: PanelWidget; refreshKey: number }) {
  const [rows, setRows] = useState<any[]>([])
  useEffect(() => {
    const source = widget.source || 'outages'
    const path = source === 'outages' ? `/outages?limit=${widget.limit || 8}` : source === 'events' ? `/events?limit=${widget.limit || 8}` : `/devices?limit=${widget.limit || 8}`
    apiGet<any[]>(path).then(setRows).catch(() => setRows([]))
  }, [widget.source, widget.limit, refreshKey])
  const limit = widget.limit || 8
  return (
    <div className="widget-table">
      {rows.slice(0, limit).map((r, i) => (
        <div className="widget-table-row" key={r.id || i}>
          <span className={`map-tree-status ${statusClass(r.status || (r.ended_at === null ? 'down' : r.severity === 'critical' ? 'down' : 'up'))}`} />
          <b>{r.device_name || r.name || r.device || 'Evento'}</b>
          <span>{r.error_message || r.message || r.primary_ip || r.severity || ''}</span>
          <small>{r.started_at || r.at || r.last_status_change_at ? new Date(r.started_at || r.at || r.last_status_change_at).toLocaleString('es-ES', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' }) : ''}</small>
        </div>
      ))}
      {!rows.length && <div className="widget-empty">Sin datos.</div>}
    </div>
  )
}

function MapWidget({ widget, refreshKey }: { widget: PanelWidget; refreshKey: number }) {
  const [canvas, setCanvas] = useState<CanvasData | null>(null)
  useEffect(() => {
    if (!widget.map_id) { setCanvas(null); return }
    apiGet<CanvasData>(`/maps/${widget.map_id}/canvas`).then(setCanvas).catch(() => setCanvas(null))
  }, [widget.map_id, refreshKey])
  if (!widget.map_id) return <div className="widget-empty">Selecciona un mapa al editar el panel.</div>
  if (!canvas) return <div className="widget-empty">Mapa no disponible.</div>
  const nodes = canvas.elements.filter(e => e.element_type !== 'zone')
  if (!nodes.length) return <div className="widget-empty">Mapa vacío.</div>
  const xs = nodes.map(n => n.pos_x)
  const ys = nodes.map(n => n.pos_y)
  const minX = Math.min(...xs), maxX = Math.max(...xs.map((x, i) => x + nodes[i].width))
  const minY = Math.min(...ys), maxY = Math.max(...ys.map((y, i) => y + nodes[i].height))
  const sw = Math.max(maxX - minX, 1), sh = Math.max(maxY - minY, 1)
  const norm = (x: number, y: number) => ({ x: ((x - minX) / sw) * 900 + 50, y: ((y - minY) / sh) * 400 + 40 })
  const byID = new Map(nodes.map(n => [n.id, n]))
  return (
    <div className="widget-minimap">
      <svg viewBox="0 0 1000 500" preserveAspectRatio="none">
        {canvas.links.map(l => {
          const s = byID.get(l.source_element_id), t = byID.get(l.target_element_id)
          if (!s || !t) return null
          const sp = norm(s.pos_x + s.width / 2, s.pos_y + s.height / 2)
          const tp = norm(t.pos_x + t.width / 2, t.pos_y + t.height / 2)
          return <line key={l.id} x1={sp.x} y1={sp.y} x2={tp.x} y2={tp.y} stroke={l.status === 'down' ? '#dc2626' : '#cbd5e1'} strokeWidth={2} />
        })}
      </svg>
      {nodes.map(n => {
        const p = norm(n.pos_x, n.pos_y)
        return (
          <div key={n.id} title={n.rendered_label} className={`widget-map-node ${statusClass(n.status)}`}
            style={{ left: `${(p.x / 1000) * 100}%`, top: `${(p.y / 500) * 100}%`, width: `${Math.max(42, (n.width / sw) * 76)}px` }}>
            {n.rendered_label?.split('\n')[0] || n.device?.name || 'Nodo'}
          </div>
        )
      })}
    </div>
  )
}

export default function PanelsPage() {
  const user = getUser()
  const [panels, setPanels] = useState<DashboardPanel[]>([])
  const [selected, setSelected] = useState('')
  const [summary, setSummary] = useState<DashboardSummary | null>(null)
  const [maps, setMaps] = useState<MapInfo[]>([])
  const [catalog, setCatalog] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshKey, setRefreshKey] = useState(0)
  const [noc, setNoc] = useState(false)

  const [editOpen, setEditOpen] = useState(false)
  const [editing, setEditing] = useState<DashboardPanel | null>(null)
  const [form, setForm] = useState({ name: '', description: '', widgets: [] as PanelWidget[], is_shared: false, is_noc: false })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const load = async () => {
    setLoading(true)
    try {
      const [p, s, m, c] = await Promise.all([
        apiGet<DashboardPanel[]>('/panels'),
        apiGet<DashboardSummary>('/dashboard/summary'),
        apiGet<MapInfo[]>('/maps'),
        apiGet<any[]>('/metrics/catalog').catch(() => []),
      ])
      setPanels(p); setSummary(s); setMaps(m); setCatalog(c)
      setSelected(prev => prev || p[0]?.id || '')
    } finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])
  useEffect(() => {
    const timer = window.setInterval(() => {
      setRefreshKey(k => k + 1)
      apiGet<DashboardSummary>('/dashboard/summary').then(setSummary).catch(() => {})
    }, 30000)
    return () => window.clearInterval(timer)
  }, [])

  const current = panels.find(p => p.id === selected) || null

  const openEditor = (panel?: DashboardPanel) => {
    setError('')
    setEditing(panel || null)
    setForm(panel ? { name: panel.name, description: panel.description, widgets: panel.widgets || [], is_shared: panel.is_shared, is_noc: panel.is_noc } : { name: 'NOC general', description: '', widgets: [], is_shared: false, is_noc: false })
    setEditOpen(true)
  }

  const save = async () => {
    setError(''); setSaving(true)
    try {
      if (!form.name.trim()) throw new Error('El nombre es obligatorio')
      const payload = { ...form, widgets: form.widgets }
      if (editing) await apiSend(`/panels/${editing.id}`, 'PUT', payload)
      else {
        const created = await apiSend<{ id: string }>('/panels', 'POST', payload)
        setSelected(created.id)
      }
      setEditOpen(false); setEditing(null)
      await load()
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const remove = async (panel: DashboardPanel) => {
    if (!await confirmAction(`¿Eliminar el panel "${panel.name}"?`)) return
    await apiSend(`/panels/${panel.id}`, 'DELETE')
    setSelected('')
    await load()
  }

  const updateWidget = (id: string, patch: Partial<PanelWidget>) => {
    setForm(prev => ({ ...prev, widgets: prev.widgets.map(w => w.id === id ? { ...w, ...patch } : w) }))
  }

  const addWidget = (kind: PanelWidget['kind']) => setForm(prev => ({ ...prev, widgets: [...prev.widgets, newWidget(kind)] }))
  const removeWidget = (id: string) => setForm(prev => ({ ...prev, widgets: prev.widgets.filter(w => w.id !== id) }))

  const renderedWidgets = current?.widgets || []
  const counterValue = (metric?: string) => {
    if (!summary) return '—'
    if (metric === 'up') return summary.devices.up
    if (metric === 'warning') return summary.devices.warning
    if (metric === 'down') return summary.devices.down
    if (metric === 'unreachable') return summary.devices.unreachable
    if (metric === 'outages') return summary.active_outages
    if (metric === 'devices') return summary.devices.total
    if (metric === 'probes') return summary.probes
    return '—'
  }

  if (loading) return <div className="page-center"><Spinner /></div>

  return (
    <div className={`page panels-page ${noc ? 'is-noc' : ''}`}>
      <div className="page-head">
        <div>
          <span className="eyebrow">Paneles y NOC</span>
          <h1>Paneles</h1>
          <p className="page-subtitle">Widgets de gráfica, tabla, mapa, contador y texto. Reutilizan métricas, incidencias y resumen del sistema.</p>
        </div>
        <div className="page-head-actions">
          <Button variant="secondary" onClick={() => setRefreshKey(k => k + 1)}><RefreshCcw size={15} /> Refrescar</Button>
          <Button variant="secondary" onClick={() => setNoc(!noc)}>{noc ? <Minimize2 size={15} /> : <Maximize2 size={15} />} {noc ? 'Salir de NOC' : 'Modo NOC'}</Button>
          <Button variant="primary" onClick={() => openEditor()}><Plus size={15} /> Nuevo panel</Button>
        </div>
      </div>

      <div className="panels-layout">
        <aside className="panels-list">
          {panels.map(p => (
            <button key={p.id} className={`panel-list-item ${selected === p.id ? 'active' : ''}`} onClick={() => setSelected(p.id)}>
              <LayoutDashboard size={15} />
              <div><b>{p.name}</b><span>{p.owner || 'global'} · {p.widgets?.length || 0} widgets</span></div>
              {p.is_noc && <span className="unit-chip">NOC</span>}
              {p.is_shared && <span className="builtin-pill">compartido</span>}
            </button>
          ))}
          {!panels.length && <EmptyState title="Sin paneles" message="Crea un panel y añade widgets." icon={<LayoutDashboard size={22} />} />}
        </aside>

        <section className="panels-main">
          {current ? (
            <>
              <div className="panel-title-row">
                <div><h2>{current.name}</h2><p>{current.description || 'Panel sin descripción.'}</p></div>
                <div>
                  {(user?.role === 'admin' || current.user_id === user?.id) && <Button variant="secondary" size="sm" onClick={() => openEditor(current)}><Pencil size={13} /> Editar</Button>}
                  {(user?.role === 'admin' || current.user_id === user?.id) && <Button variant="danger" size="sm" onClick={() => remove(current)}><Trash2 size={13} /></Button>}
                </div>
              </div>
              <div className="widget-grid">
                {renderedWidgets.map(w => (
                  <div className={`panel-widget widget-${w.kind} ${w.kind === 'chart' || w.kind === 'table' ? 'wide' : ''}`} key={w.id}>
                    <div className="widget-head"><b>{w.title}</b><span className="widget-kind">{w.kind}</span></div>
                    {w.kind === 'chart' && <ChartWidget widget={w} refreshKey={refreshKey} />}
                    {w.kind === 'table' && <TableWidget widget={w} refreshKey={refreshKey} />}
                    {w.kind === 'map' && <MapWidget widget={w} refreshKey={refreshKey} />}
                    {w.kind === 'counter' && <div className="widget-counter"><b>{counterValue(w.metric)}</b><span>{w.metric || 'métrica'}</span></div>}
                    {w.kind === 'text' && <div className="widget-text">{w.text || 'Sin texto.'}</div>}
                  </div>
                ))}
                {!renderedWidgets.length && <EmptyState title="Panel vacío" message="Edita el panel para añadir widgets." icon={<BarChart3 size={24} />} />}
              </div>
            </>
          ) : <EmptyState title="Selecciona un panel" message="Crea un panel NOC o selecciona uno existente." icon={<LayoutDashboard size={24} />} />}
        </section>
      </div>

      <Modal open={editOpen} onClose={() => { setEditOpen(false); setEditing(null) }} title={editing ? 'Editar panel' : 'Nuevo panel'} subtitle="Los widgets se refrescan automáticamente." width={860}
        footer={<><Button variant="ghost" onClick={() => setEditOpen(false)}>Cancelar</Button><Button variant="primary" onClick={save} disabled={saving}>{saving ? 'Guardando…' : 'Guardar panel'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} /></Field>
          <Field label="Descripción"><input className="input" value={form.description} onChange={e => setForm({ ...form, description: e.target.value })} /></Field>
        </div>
        <div className="checkbox-row">
          <label className="check-label"><input type="checkbox" checked={form.is_shared} onChange={e => setForm({ ...form, is_shared: e.target.checked })} /> Compartido con todos</label>
          <label className="check-label"><input type="checkbox" checked={form.is_noc} onChange={e => setForm({ ...form, is_noc: e.target.checked })} /> Panel de guardia NOC</label>
        </div>
        <div className="widget-editor-add">
          <span className="field-label">Añadir widget</span>
          <Button variant="secondary" size="sm" onClick={() => addWidget('chart')}><BarChart3 size={13} /> Gráfica</Button>
          <Button variant="secondary" size="sm" onClick={() => addWidget('table')}><List size={13} /> Tabla</Button>
          <Button variant="secondary" size="sm" onClick={() => addWidget('map')}><MapIcon size={13} /> Mapa</Button>
          <Button variant="secondary" size="sm" onClick={() => addWidget('counter')}><Check size={13} /> Contador</Button>
          <Button variant="secondary" size="sm" onClick={() => addWidget('text')}><Type size={13} /> Texto</Button>
        </div>
        <div className="widget-editor-list">
          {form.widgets.map(w => (
            <div className="widget-editor-row" key={w.id}>
              <div className="widget-editor-head">
                <span className="unit-chip">{w.kind}</span>
                <input className="input" value={w.title} onChange={e => updateWidget(w.id, { title: e.target.value })} />
                <Button variant="danger" size="sm" onClick={() => removeWidget(w.id)}><Trash2 size={12} /></Button>
              </div>
              {w.kind === 'chart' && (
                <div className="form-grid two">
                  <Field label="Series (Ctrl/Cmd para varias)">
                    <select multiple className="input" size={5} value={w.series || []} onChange={e => updateWidget(w.id, { series: Array.from(e.target.selectedOptions).map(o => o.value) })}>
                      {catalog.map(s => <option key={s.id} value={s.id}>{s.label} · {s.unit || s.kind}</option>)}
                    </select>
                  </Field>
                  <Field label="Rango"><select className="input" value={w.range || '24h'} onChange={e => updateWidget(w.id, { range: e.target.value })}>{RANGES.map(r => <option key={r} value={r}>{r}</option>)}</select></Field>
                </div>
              )}
              {w.kind === 'table' && (
                <div className="form-grid two">
                  <Field label="Fuente"><select className="input" value={w.source || 'outages'} onChange={e => updateWidget(w.id, { source: e.target.value })}><option value="outages">Incidencias</option><option value="events">Eventos</option><option value="devices">Dispositivos</option></select></Field>
                  <Field label="Filas"><input className="input" type="number" min={3} max={30} value={w.limit || 8} onChange={e => updateWidget(w.id, { limit: Number(e.target.value) })} /></Field>
                </div>
              )}
              {w.kind === 'map' && (
                <Field label="Mapa"><select className="input" value={w.map_id || ''} onChange={e => updateWidget(w.id, { map_id: e.target.value })}><option value="">— Selecciona</option>{maps.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}</select></Field>
              )}
              {w.kind === 'counter' && (
                <Field label="Métrica"><select className="input" value={w.metric || 'up'} onChange={e => updateWidget(w.id, { metric: e.target.value })}><option value="up">Operativos</option><option value="warning">Avisos</option><option value="down">Caídos</option><option value="unreachable">Aislados</option><option value="outages">Incidencias activas</option><option value="devices">Dispositivos totales</option><option value="probes">Sondas</option></select></Field>
              )}
              {w.kind === 'text' && (
                <Field label="Texto"><textarea className="input textarea" rows={3} value={w.text || ''} onChange={e => updateWidget(w.id, { text: e.target.value })} /></Field>
              )}
            </div>
          ))}
          {!form.widgets.length && <div className="widget-empty">Añade el primer widget.</div>}
        </div>
        {error && <div className="form-error">{error}</div>}
      </Modal>
    </div>
  )
}
