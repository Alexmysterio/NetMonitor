import { useEffect, useMemo, useState } from 'react'
import { CalendarRange, LineChart as LineChartIcon, RefreshCcw, Search } from 'lucide-react'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { apiGet } from '../api'
import type { MetricPoint, MetricSeries } from '../types'
import { Button, EmptyState, Field, Spinner, formatBps } from '../ui'

const COLORS = ['#2563eb', '#0891b2', '#7c3aed', '#16a34a', '#f59e0b', '#dc2626', '#0f766e', '#db2777', '#64748b', '#ea580c', '#4f46e5', '#059669']
const RANGES = ['1h', '6h', '24h', '7d', '30d']

type CatalogEntry = { id: string; kind: string; label: string; unit: string; device_name?: string; probe_type?: string }

export default function HistoryPage() {
  const [catalog, setCatalog] = useState<CatalogEntry[]>([])
  const [selected, setSelected] = useState<string[]>([])
  const [range, setRange] = useState('24h')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [custom, setCustom] = useState(false)
  const [series, setSeries] = useState<MetricSeries[]>([])
  const [q, setQ] = useState('')
  const [loading, setLoading] = useState(true)
  const [chartLoading, setChartLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    apiGet<CatalogEntry[]>('/metrics/catalog')
      .then(data => {
        setCatalog(data)
        const first = data.find(x => x.kind === 'service')
        if (first) setSelected([first.id])
      })
      .catch(e => setError((e as Error).message))
      .finally(() => setLoading(false))
  }, [])

  const loadSeries = async () => {
    if (!selected.length) { setSeries([]); return }
    setChartLoading(true); setError('')
    try {
      const params = new URLSearchParams({ series: selected.join(',') })
      if (custom && from && to) {
        params.set('from', new Date(from).toISOString())
        params.set('to', new Date(to).toISOString())
      } else {
        params.set('range', range)
      }
      const data = await apiGet<{ series: MetricSeries[] }>(`/metrics/compare?${params.toString()}`)
      setSeries(data.series || [])
    } catch (e) { setError((e as Error).message) } finally { setChartLoading(false) }
  }

  useEffect(() => { void loadSeries() }, [selected.join(','), range, custom, from, to])

  const chart = useMemo(() => {
    const byTime = new Map<string, any>()
    series.forEach((s, i) => {
      s.points.forEach(p => {
        const row = byTime.get(p.time) || { time: p.time }
        row[`s${i}`] = p.value
        byTime.set(p.time, row)
      })
    })
    return Array.from(byTime.values()).sort((a, b) => a.time.localeCompare(b.time))
  }, [series])
  const isBps = series.some(s => s.unit === 'bps')
  const filtered = catalog.filter(c => !q || c.label.toLowerCase().includes(q.toLowerCase()))

  const toggle = (id: string) => {
    setSelected(prev => prev.includes(id) ? prev.filter(x => x !== id) : prev.length >= 12 ? prev : [...prev, id])
  }

  if (loading) return <div className="page-center"><Spinner /></div>

  return (
    <div className="page history-page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Histórico global</span>
          <h1>Gráficas y comparación</h1>
          <p className="page-subtitle">Selecciona servicios y enlaces, compara varias series y elige rangos personalizados.</p>
        </div>
        <div className="page-head-actions">
          <Button variant="secondary" onClick={loadSeries} disabled={chartLoading}><RefreshCcw size={15} /> {chartLoading ? 'Cargando…' : 'Actualizar'}</Button>
        </div>
      </div>

      <div className="history-layout">
        <aside className="history-picker">
          <div className="search-inline"><Search size={14} /><input className="input" value={q} onChange={e => setQ(e.target.value)} placeholder="Filtrar series…" /></div>
          <div className="history-series-list">
            {filtered.map(c => (
              <label key={c.id} className={`history-series-item ${selected.includes(c.id) ? 'active' : ''}`}>
                <input type="checkbox" checked={selected.includes(c.id)} onChange={() => toggle(c.id)} />
                <span className={`history-kind ${c.kind}`}>{c.kind === 'link' ? 'LINK' : c.kind === 'device' ? 'DEV' : 'SVC'}</span>
                <span>{c.label}</span>
                {c.unit && <small>{c.unit}</small>}
              </label>
            ))}
            {!filtered.length && <div className="muted small">Sin series disponibles.</div>}
          </div>
        </aside>

        <section className="history-main">
          <div className="history-controls">
            {RANGES.map(r => (
              <button key={r} className={`range-btn ${!custom && range === r ? 'active' : ''}`} onClick={() => { setCustom(false); setRange(r) }}>{r}</button>
            ))}
            <button className={`range-btn ${custom ? 'active' : ''}`} onClick={() => setCustom(true)}><CalendarRange size={13} /> Personalizado</button>
            {custom && (
              <div className="history-custom-range">
                <Field label="Desde"><input className="input" type="datetime-local" value={from} onChange={e => setFrom(e.target.value)} /></Field>
                <Field label="Hasta"><input className="input" type="datetime-local" value={to} onChange={e => setTo(e.target.value)} /></Field>
              </div>
            )}
          </div>
          {error && <div className="form-error">{error}</div>}
          <div className="history-chart-card">
            <div className="chart-head"><span><LineChartIcon size={14} /> {selected.length} series seleccionadas</span><span className="mono muted">{custom ? 'rango personalizado' : range}</span></div>
            {chartLoading ? <div className="chart-empty"><Spinner label="Consultando histórico" /></div> : chart.length ? (
              <ResponsiveContainer width="100%" height={360}>
                <LineChart data={chart} margin={{ top: 10, right: 20, left: 0, bottom: 0 }}>
                  <CartesianGrid stroke="#eef2f7" vertical={false} />
                  <XAxis dataKey="time" tick={{ fontSize: 9, fill: '#94a3b8' }} axisLine={false} tickLine={false} tickFormatter={v => new Date(v).toLocaleString('es-ES', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })} minTickGap={40} />
                  <YAxis tick={{ fontSize: 9, fill: '#94a3b8' }} axisLine={false} tickLine={false} width={isBps ? 66 : 46} tickFormatter={v => isBps ? formatBps(Number(v)) : Number(v).toFixed(0)} />
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
            ) : <EmptyState title="Sin datos" message="Selecciona una serie y un rango con histórico disponible." icon={<LineChartIcon size={24} />} />}
          </div>
          <div className="history-legend">
            {series.map((s, i) => {
              const last = s.points[s.points.length - 1]?.value
              return <div key={s.id}><i style={{ background: COLORS[i % COLORS.length] }} /><span>{s.label}</span><b>{last == null ? '—' : s.unit === 'bps' ? formatBps(last) : `${last.toFixed(2)} ${s.unit}`}</b></div>
            })}
          </div>
        </section>
      </div>
    </div>
  )
}
