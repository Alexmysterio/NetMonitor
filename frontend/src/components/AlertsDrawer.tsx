import { useEffect, useState } from 'react'
import { AlertTriangle, ArrowUpRight, Check, ChevronDown, ChevronUp, Clock3, MapPin, Radio } from 'lucide-react'
import { apiGet, apiSend } from '../api'
import { useRealtime } from '../realtime'
import type { EventItem, Outage } from '../types'
import { Button, formatDuration, StatusBadge, timeAgo, timeHM } from '../ui'

export default function AlertsDrawer({
  currentMapId,
  onNavigate,
  onRefresh,
}: {
  currentMapId: string
  onNavigate: (id: string) => void
  onRefresh: () => void
}) {
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState<'outages' | 'events'>('outages')
  const [outages, setOutages] = useState<Outage[]>([])
  const [events, setEvents] = useState<EventItem[]>([])
  const [acking, setAcking] = useState<string | null>(null)
  const { refreshKey } = useRealtime()

  const load = async () => {
    try {
      const [o, e] = await Promise.all([
        apiGet<Outage[]>('/outages/active'),
        apiGet<EventItem[]>('/events'),
      ])
      setOutages(o)
      setEvents(e)
    } catch {
      /* noop */
    }
  }

  useEffect(() => {
    void load()
    const timer = window.setInterval(load, 15000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    if (refreshKey) void load()
  }, [refreshKey])

  const acknowledge = async (o: Outage) => {
    setAcking(o.id)
    try {
      await apiSend(`/outages/${o.id}/acknowledge`, 'POST', { note: 'Reconocido desde el panel NOC' })
      await apiSend(`/devices/${o.device_id}/acknowledge`, 'POST', { note: 'Reconocido desde el panel NOC', duration_minutes: 0 })
      window.dispatchEvent(new Event('netmonitor:maps-changed'))
      await load()
      onRefresh()
    } finally {
      setAcking(null)
    }
  }

  const critical = outages.filter(o => !o.acknowledged && (o.severity === 'critical' || o.severity === 'down')).length
  const acknowledged = outages.filter(o => o.acknowledged).length
  const allAcknowledged = outages.length > 0 && acknowledged === outages.length

  return (
    <div className={`alerts-drawer ${open ? 'open' : ''}`}>
      <button className="alerts-head" onClick={() => setOpen(!open)}>
        <span className={`alerts-pulse ${critical ? 'critical' : allAcknowledged ? 'ack' : outages.length ? 'warning' : 'ok'}`} />
        <span className="alerts-title">Incidencias activas</span>
        <span className="alerts-count">{outages.length}</span>
        {outages.length > 0 && <span className="alerts-latest">{outages[0].device_name} · {timeAgo(outages[0].started_at)}</span>}
        {!outages.length && <span className="alerts-latest">Sin caídas activas · la red está estable</span>}
        <span className="alerts-spacer" />
        <span className="alerts-live"><Radio size={12} /> en vivo</span>
        {open ? <ChevronDown size={16} /> : <ChevronUp size={16} />}
      </button>

      {open && (
        <div className="alerts-body">
          <div className="alerts-tabs">
            <button className={tab === 'outages' ? 'active' : ''} onClick={() => setTab('outages')}>Incidencias activas <span>{outages.length}</span></button>
            <button className={tab === 'events' ? 'active' : ''} onClick={() => setTab('events')}>Últimos eventos <span>{events.length}</span></button>
          </div>
          {tab === 'outages' && (
            <div className="outage-list">
              {outages.map(o => (
                <div key={o.id} className="outage-row">
                  <StatusBadge status={o.acknowledged ? 'acknowledged' : o.severity === 'critical' ? 'down' : o.severity} />
                  <div className="outage-main">
                    <b>{o.device_name}</b>
                    <span className="mono">{o.primary_ip}</span>
                  </div>
                  <div className="outage-map"><MapPin size={12} /> {o.map_name || 'Sin mapa'}</div>
                  <div className="outage-duration"><Clock3 size={12} /> {formatDuration(o.duration_sec)}</div>
                  <div className="outage-actions">
                    {o.map_id && o.map_id !== currentMapId && <button onClick={() => onNavigate(o.map_id)} title="Ir al mapa"><ArrowUpRight size={14} /></button>}
                    <Button variant="secondary" size="sm" disabled={o.acknowledged || acking === o.id} onClick={() => acknowledge(o)}>
                      <Check size={13} /> {o.acknowledged ? 'Reconocida' : 'Reconocer'}
                    </Button>
                  </div>
                </div>
              ))}
              {!outages.length && <div className="outage-empty"><AlertTriangle size={18} /> No hay incidencias activas. Todos los servicios monitorizados responden.</div>}
            </div>
          )}
          {tab === 'events' && (
            <div className="event-list">
              {events.slice(0, 12).map(e => (
                <div className="event-row" key={e.id}>
                  <span className={`event-dot status-${e.severity === 'critical' ? 'down' : e.severity === 'warning' ? 'warning' : e.severity === 'ok' ? 'up' : 'unknown'}`} />
                  <span className="event-time mono">{timeHM(e.at)}</span>
                  <span className="event-device">{e.device_name || 'Sistema'}</span>
                  <span className="event-msg">{e.message}</span>
                  {e.map_name && <span className="event-map">{e.map_name}</span>}
                </div>
              ))}
              {!events.length && <div className="outage-empty">No hay eventos registrados todavía.</div>}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
