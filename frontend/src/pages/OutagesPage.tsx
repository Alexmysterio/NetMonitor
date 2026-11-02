import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Check, ChevronRight, Clock3, MapPin, ShieldAlert } from 'lucide-react'
import { apiGet, apiSend } from '../api'
import type { Outage } from '../types'
import { Button, EmptyState, formatDuration, Spinner, StatusBadge, timeAgo } from '../ui'

export default function OutagesPage() {
  const [outages, setOutages] = useState<Outage[]>([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState<'active' | 'all'>('active')
  const [acking, setAcking] = useState('')
  const navigate = useNavigate()

  const load = async () => {
    setLoading(true)
    try {
      setOutages(await apiGet<Outage[]>('/outages'))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { void load() }, [])

  const list = useMemo(() => filter === 'active' ? outages.filter(o => !o.ended_at) : outages, [outages, filter])
  const active = outages.filter(o => !o.ended_at)
  const totalActive = active.reduce((acc, o) => acc + o.duration_sec, 0)

  const ack = async (o: Outage) => {
    setAcking(o.id)
    try {
      await apiSend(`/outages/${o.id}/acknowledge`, 'POST', { note: 'Reconocido desde incidencias' })
      await apiSend(`/devices/${o.device_id}/acknowledge`, 'POST', { note: 'Reconocido desde incidencias', duration_minutes: 0 })
      await load()
    } finally {
      setAcking('')
    }
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Histórico</span>
          <h1>Caídas e incidencias</h1>
          <p className="page-subtitle">Registro forense de indisponibilidad, MTTR y reconocimiento de alertas.</p>
        </div>
        <div className="page-head-actions">
          <div className="page-stat"><b className="danger">{active.length}</b><span>activas</span></div>
          <div className="page-stat"><b>{formatDuration(totalActive)}</b><span>tiempo acumulado</span></div>
        </div>
      </div>

      <div className="table-card">
        <div className="table-toolbar">
          <div className="filter-chips">
            <button className={`chip ${filter === 'active' ? 'active' : ''}`} onClick={() => setFilter('active')}>Activas <span>{active.length}</span></button>
            <button className={`chip ${filter === 'all' ? 'active' : ''}`} onClick={() => setFilter('all')}>Todas <span>{outages.length}</span></button>
          </div>
        </div>
        {loading ? <div className="page-center"><Spinner /></div> : (
          <div className="data-table outages-table">
            <div className="data-row head">
              <span>Severidad</span><span>Dispositivo</span><span>Inicio</span><span>Duración</span><span>Mapa</span><span>Estado</span><span />
            </div>
            {list.map(o => (
              <div className="data-row" key={o.id}>
                <span><StatusBadge status={o.severity === 'critical' ? 'down' : o.severity} /></span>
                <span className="cell-main"><b>{o.device_name}</b><small className="mono">{o.primary_ip}</small>{o.root_cause && <small className="correlation-pill">Causa: {o.root_cause}</small>}{o.suppress_notifications && <small className="suppressed-pill">Alarma suprimida</small>}</span>
                <span className="muted small">{timeAgo(o.started_at)}</span>
                <span className="mono">{formatDuration(o.duration_sec)}</span>
                <span className="cell-map">{o.map_name && <button onClick={() => o.map_id && navigate(`/maps/${o.map_id}`)}><MapPin size={12} />{o.map_name}</button>}</span>
                <span>{o.ended_at ? <span className="resolved-pill"><Check size={11} /> Resuelta</span> : o.acknowledged ? <span className="ack-pill">ACK {o.acknowledged_by}</span> : <span className="active-pill"><span /> Activa</span>}</span>
                <span>
                  {!o.ended_at && !o.acknowledged && (
                    <Button size="sm" variant="secondary" disabled={acking === o.id} onClick={() => ack(o)}>
                      <Check size={13} /> {acking === o.id ? '…' : 'Ack'}
                    </Button>
                  )}
                </span>
              </div>
            ))}
            {!list.length && !loading && <EmptyState title="Sin incidencias" message={filter === 'active' ? 'No hay caídas activas en este momento.' : 'Todavía no se ha registrado ninguna caída.'} icon={<ShieldAlert size={24} />} />}
          </div>
        )}
      </div>
    </div>
  )
}
