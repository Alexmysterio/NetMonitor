import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Activity, AlertTriangle, Check, ChevronRight, Database, Pencil, Plus, RefreshCcw, Search, Server, Tag, Trash2, Wrench, X } from 'lucide-react'
import { apiGet, apiSend } from '../api'
import { confirmAction } from '../confirm'
import type { Agent, Device, DeviceService, SnmpProfile } from '../types'
import { Button, deviceTypeLabel, EmptyState, Field, formatDuration, IconButton, KeyValue, Modal, Spinner, StatusBadge, StatusDot, timeAgo, timeHM } from '../ui'
import SnmpInfoModal from '../components/SnmpInfoModal'
import ToolsModal from '../components/ToolsModal'

export default function DevicesPage() {
  const [params] = useSearchParams()
  const [devices, setDevices] = useState<Device[]>([])
  const [loading, setLoading] = useState(true)
  const [q, setQ] = useState(params.get('q') || '')
  const [status, setStatus] = useState('')
  const [selected, setSelected] = useState<Device | null>(null)
  const [detail, setDetail] = useState<Device | null>(null)
  const [bulkSelected, setBulkSelected] = useState<string[]>([])
  const [bulkAction, setBulkAction] = useState('snmp_profile')
  const [bulkProfileID, setBulkProfileID] = useState('')
  const [bulkTag, setBulkTag] = useState('')
  const [bulkBusy, setBulkBusy] = useState(false)
  const [bulkError, setBulkError] = useState('')
  const [notice, setNotice] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [name, setName] = useState('')
  const [ip, setIp] = useState('')
  const [secondary, setSecondary] = useState('')
  const [type, setType] = useState('router')
  const [profile, setProfile] = useState('')
  const [profiles, setProfiles] = useState<SnmpProfile[]>([])
  const [deviceTypes, setDeviceTypes] = useState<any[]>([])
  const typeLabel = (name: string) => deviceTypes.find((t: any) => t.name === name)?.label || deviceTypeLabel(name)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [editOpen, setEditOpen] = useState(false)
  const [snmpOpen, setSnmpOpen] = useState(false)
  const [toolsOpen, setToolsOpen] = useState(false)
  const [allDevices, setAllDevices] = useState<Device[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [edit, setEdit] = useState({
    name: '', primary_ip: '', secondary_ips: '', dns_name: '', device_type: 'router', snmp_profile_id: '', agent_id: '',
    tags: '', notes: '', parent_ids: [] as string[], monitoring: true,
    refreshCustom: false, sysRefresh: 300, ifRefresh: 60, ipRefresh: 300, arpRefresh: 300, routeRefresh: 600,
    poll_interval_sec: 0, poll_down_retries: 0, poll_agent_id: '', poll_notifications: -1,
  })

  const load = async () => {
    setLoading(true)
    try {
      const query = new URLSearchParams()
      if (q) query.set('q', q)
      if (status) query.set('status', status)
      const data = await apiGet<Device[]>(`/devices?${query.toString()}`)
      setDevices(data)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    const timer = window.setTimeout(load, 200)
    return () => window.clearTimeout(timer)
  }, [q, status])

  useEffect(() => {
    apiGet<any[]>('/device-types').then(setDeviceTypes).catch(() => {})
    apiGet<SnmpProfile[]>('/snmp-profiles').then(setProfiles).catch(() => {})
  }, [])

  useEffect(() => {
    setBulkSelected(prev => {
      const visible = new Set(devices.map(d => d.id))
      const next = prev.filter(id => visible.has(id))
      return next.length === prev.length ? prev : next
    })
  }, [devices])

  useEffect(() => {
    if (!createOpen) return
    apiGet<SnmpProfile[]>('/snmp-profiles').then(p => {
      setProfiles(p)
      if (p[0]) setProfile(p[0].id)
    }).catch(() => {})
  }, [createOpen])

  const openDetail = async (d: Device) => {
    setSelected(d)
    setError('')
    const full = await apiGet<Device>(`/devices/${d.id}`)
    setDetail(full)
  }

  const create = async () => {
    setError('')
    setSaving(true)
    try {
      if (!name.trim() || !ip.trim()) throw new Error('Nombre e IP son obligatorios')
      await apiSend('/devices', 'POST', {
        name, primary_ip: ip, secondary_ips: secondary.split(',').map(v => v.trim()).filter(Boolean),
        device_type: type, snmp_profile_id: profile || null, create_default_ping: true,
      })
      setCreateOpen(false)
      setName(''); setIp(''); setSecondary(''); setType('router')
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const poll = async (id: string) => {
    await apiSend(`/devices/${id}/poll-now`, 'POST')
    if (detail) {
      const full = await apiGet<Device>(`/devices/${id}`)
      setDetail(full)
      setSelected(full)
    }
  }

  const openEdit = async (d: Device) => {
    const [full, list, profs, ags] = await Promise.all([
      apiGet<Device>(`/devices/${d.id}`),
      apiGet<Device[]>('/devices'),
      apiGet<SnmpProfile[]>('/snmp-profiles'),
      apiGet<Agent[]>('/agents'),
    ])
    setAllDevices(list.filter(x => x.id !== d.id))
    setProfiles(profs)
    setAgents(ags)
    setEdit({
      name: full.name,
      primary_ip: full.primary_ip,
      secondary_ips: (full.secondary_ips || []).join(', '),
      dns_name: full.dns_name || '',
      device_type: full.device_type,
      snmp_profile_id: full.snmp_profile_id || '',
      agent_id: full.agent_id || '',
      tags: (full.tags || []).join(', '),
      notes: full.notes || '',
      parent_ids: full.parent_ids || [],
      monitoring: full.is_monitoring_enabled,
      refreshCustom: Object.keys(full.snmp_refresh?.overrides || {}).length > 0,
      sysRefresh: full.snmp_refresh?.overrides?.system_sec ?? full.snmp_refresh?.system_sec ?? 300,
      ifRefresh: full.snmp_refresh?.overrides?.interfaces_sec ?? full.snmp_refresh?.interfaces_sec ?? 60,
      ipRefresh: full.snmp_refresh?.overrides?.ips_sec ?? full.snmp_refresh?.ips_sec ?? 300,
      arpRefresh: full.snmp_refresh?.overrides?.arp_sec ?? full.snmp_refresh?.arp_sec ?? 300,
      routeRefresh: full.snmp_refresh?.overrides?.routes_sec ?? full.snmp_refresh?.routes_sec ?? 600,
      poll_interval_sec: full.poll_interval_sec || 0,
      poll_down_retries: full.poll_down_retries || 0,
      poll_agent_id: full.poll_agent_id || '',
      poll_notifications: full.poll_notifications ?? -1,
    })
    setEditOpen(true)
  }

  const saveEdit = async () => {
    if (!detail) return
    setError('')
    setSaving(true)
    try {
      await apiSend(`/devices/${detail.id}`, 'PUT', {
        name: edit.name,
        primary_ip: edit.primary_ip,
        secondary_ips: edit.secondary_ips.split(',').map(v => v.trim()).filter(Boolean),
        dns_name: edit.dns_name,
        device_type: edit.device_type,
        snmp_profile_id: edit.snmp_profile_id,
        agent_id: edit.agent_id,
        tags: edit.tags.split(',').map(t => t.trim()).filter(Boolean),
        notes: edit.notes,
        parent_ids: edit.parent_ids,
        is_monitoring_enabled: edit.monitoring,
        snmp_system_refresh_sec: edit.refreshCustom ? edit.sysRefresh : -1,
        snmp_interfaces_refresh_sec: edit.refreshCustom ? edit.ifRefresh : -1,
        snmp_ips_refresh_sec: edit.refreshCustom ? edit.ipRefresh : -1,
        snmp_arp_refresh_sec: edit.refreshCustom ? edit.arpRefresh : -1,
        snmp_routes_refresh_sec: edit.refreshCustom ? edit.routeRefresh : -1,
        poll_interval_sec: edit.poll_interval_sec,
        poll_down_retries: edit.poll_down_retries,
        poll_agent_id: edit.poll_agent_id,
        poll_notifications: edit.poll_notifications,
      })
      setEditOpen(false)
      const full = await apiGet<Device>(`/devices/${detail.id}`)
      setDetail(full)
      setSelected(full)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const ack = async (d: Device) => {
    if (d.is_acknowledged) await apiSend(`/devices/${d.id}/acknowledge`, 'DELETE')
    else await apiSend(`/devices/${d.id}/acknowledge`, 'POST', { note: 'Ack desde inventario', duration_minutes: 0 })
    const full = await apiGet<Device>(`/devices/${d.id}`)
    setDetail(full)
    setSelected(full)
    await load()
  }

  const removeDevice = async (d: Device) => {
    if (!await confirmAction(`¿Eliminar el dispositivo "${d.name}"? Se quitarán también sus elementos de los mapas, sondas e incidencias.`, { title: 'Eliminar dispositivo', confirmLabel: 'Eliminar', danger: true })) return
    setError('')
    setSaving(true)
    try {
      await apiSend(`/devices/${d.id}`, 'DELETE')
      setSelected(null)
      setDetail(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const allBulkSelected = devices.length > 0 && devices.every(d => bulkSelected.includes(d.id))

  const toggleBulk = (id: string) => {
    setBulkSelected(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])
  }

  const toggleAllBulk = () => {
    setBulkSelected(allBulkSelected ? [] : devices.map(d => d.id))
  }

  const flash = (msg: string) => {
    setNotice(msg)
    window.setTimeout(() => setNotice(''), 4000)
  }

  const runBulk = async () => {
    if (!bulkSelected.length) return
    setBulkError('')
    if (bulkAction === 'delete') {
      if (!await confirmAction(`¿Eliminar ${bulkSelected.length} dispositivos? Se quitarán también sus elementos de mapas, sondas e incidencias.`, { title: 'Eliminar dispositivos', confirmLabel: 'Eliminar', danger: true })) return
    }
    if ((bulkAction === 'tags_add' || bulkAction === 'tags_remove') && !bulkTag.trim()) {
      setBulkError('Escribe una etiqueta')
      return
    }
    if (bulkAction === 'snmp_profile' && !bulkProfileID) {
      if (!await confirmAction(`¿Quitar el perfil SNMP a ${bulkSelected.length} dispositivos?`, { title: 'Quitar perfil SNMP', confirmLabel: 'Quitar', danger: true })) return
    }
    setBulkBusy(true)
    try {
      const action = bulkAction === 'monitoring_on' || bulkAction === 'monitoring_off' ? 'monitoring' : bulkAction
      const payload: Record<string, unknown> = { device_ids: bulkSelected, action }
      if (bulkAction === 'snmp_profile') payload.snmp_profile_id = bulkProfileID
      if (bulkAction === 'tags_add' || bulkAction === 'tags_remove') payload.tags = [bulkTag.trim()]
      if (action === 'monitoring') payload.enabled = bulkAction === 'monitoring_on'
      const res = await apiSend<{ updated?: number; deleted?: number }>('/devices/bulk', 'POST', payload)
      flash(bulkAction === 'delete'
        ? `${res.deleted ?? bulkSelected.length} dispositivos eliminados`
        : `${res.updated ?? bulkSelected.length} dispositivos actualizados`)
      if (bulkAction === 'delete') {
        setSelected(null)
        setDetail(null)
      }
      setBulkSelected([])
      await load()
    } catch (e) {
      setBulkError((e as Error).message)
    } finally {
      setBulkBusy(false)
    }
  }

  const counts = useMemo(() => {
    return {
      up: devices.filter(d => d.status === 'up').length,
      warning: devices.filter(d => d.status === 'warning').length,
      down: devices.filter(d => d.status === 'down' || d.status === 'acknowledged').length,
    }
  }, [devices])

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Inventario</span>
          <h1>Dispositivos</h1>
          <p className="page-subtitle">Gestiona los equipos monitorizados, sus perfiles SNMP y sus sondas asociadas.</p>
        </div>
        <div className="page-head-actions">
          <div className="page-stat"><b className="ok">{counts.up}</b><span>operativos</span></div>
          <div className="page-stat"><b className="warn">{counts.warning}</b><span>avisos</span></div>
          <div className="page-stat"><b className="danger">{counts.down}</b><span>caídos</span></div>
          <Button variant="primary" onClick={() => setCreateOpen(true)}><Plus size={15} /> Nuevo dispositivo</Button>
        </div>
      </div>

      {bulkError && <div className="form-error"><AlertTriangle size={14} /> {bulkError}</div>}
      {notice && <div className="notice-ok"><Check size={14} /> {notice}</div>}

      <div className="table-card">
        <div className="table-toolbar">
          <div className="search-inline wide"><Search size={15} /><input className="input" value={q} onChange={e => setQ(e.target.value)} placeholder="Buscar por nombre, IP o etiqueta…" /></div>
          <div className="filter-chips">
            {['', 'up', 'warning', 'down', 'unreachable', 'disabled'].map(s => (
              <button key={s} className={`chip ${status === s ? 'active' : ''}`} onClick={() => setStatus(s)}>
                {s === '' ? 'Todos' : s === 'up' ? 'Operativos' : s === 'warning' ? 'Avisos' : s === 'down' ? 'Caídos' : s === 'unreachable' ? 'Aislados' : 'Deshabilitados'}
              </button>
            ))}
          </div>
        </div>

        {bulkSelected.length > 0 && (
          <div className="bulk-bar">
            <span className="bulk-count"><Check size={14} /> <b>{bulkSelected.length}</b> seleccionados</span>
            <select className="input bulk-action" aria-label="Acción masiva" value={bulkAction} onChange={e => setBulkAction(e.target.value)}>
              <option value="snmp_profile">Asignar perfil SNMP</option>
              <option value="tags_add">Añadir etiqueta</option>
              <option value="tags_remove">Quitar etiqueta</option>
              <option value="monitoring_on">Activar monitorización</option>
              <option value="monitoring_off">Pausar monitorización</option>
              <option value="acknowledge">Reconocer</option>
              <option value="unacknowledge">Quitar reconocimiento</option>
              <option value="poll_now">Sondear ahora</option>
              <option value="delete">Eliminar</option>
            </select>
            {bulkAction === 'snmp_profile' && (
              <select className="input bulk-profile" aria-label="Perfil SNMP" value={bulkProfileID} onChange={e => setBulkProfileID(e.target.value)}>
                <option value="">Sin perfil</option>
                {profiles.map(p => <option key={p.id} value={p.id}>{p.name} · {p.version}</option>)}
              </select>
            )}
            {bulkAction === 'snmp_profile' && !profiles.length && <span className="muted small">No hay perfiles SNMP</span>}
            {(bulkAction === 'tags_add' || bulkAction === 'tags_remove') && (
              <input className="input bulk-tag" value={bulkTag} onChange={e => setBulkTag(e.target.value)} placeholder="Etiqueta" aria-label="Etiqueta" />
            )}
            <Button size="sm" variant={bulkAction === 'delete' ? 'danger' : 'primary'} onClick={() => void runBulk()} disabled={bulkBusy}>
              {bulkBusy ? 'Aplicando…' : bulkAction === 'delete' ? <><Trash2 size={13} /> Eliminar</> : 'Aplicar'}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setBulkSelected([])}>Limpiar</Button>
          </div>
        )}

        {loading && !devices.length ? <div className="page-center"><Spinner /></div> : (
          <div className="data-table devices-table">
            <div className="data-row head">
              <span className="cell-check"><input type="checkbox" checked={allBulkSelected} onChange={toggleAllBulk} disabled={!devices.length} aria-label="Seleccionar todos los dispositivos" /></span>
              <span>Estado</span><span>Dispositivo</span><span>Dirección</span><span>Tipo</span><span>Sondas</span><span>Etiquetas</span><span>Última vez</span><span />
            </div>
            {devices.map(d => (
              <div
                key={d.id}
                className="data-row device-row"
                role="button"
                tabIndex={0}
                onClick={() => void openDetail(d)}
                onKeyDown={e => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    void openDetail(d)
                  }
                }}
              >
                <span className="cell-check" onClick={e => e.stopPropagation()} onKeyDown={e => e.stopPropagation()}>
                  <input type="checkbox" checked={bulkSelected.includes(d.id)} onChange={() => toggleBulk(d.id)} aria-label={`Seleccionar ${d.name}`} />
                </span>
                <span><StatusBadge status={d.is_acknowledged && d.status === 'down' ? 'acknowledged' : d.status} /></span>
                <span className="cell-main"><b>{d.name}</b>{d.is_monitoring_enabled === false && <small>monitorización pausada</small>}</span>
                <span className="mono cell-ip">{d.primary_ip}</span>
                <span className="cell-type">{typeLabel(d.device_type)}</span>
                <span className="cell-services">
                  <i className="dot up" />{d.services_summary?.up ?? 0}
                  {(d.services_summary?.warning ?? 0) > 0 && <><i className="dot warn" />{d.services_summary?.warning}</>}
                  {(d.services_summary?.down ?? 0) > 0 && <><i className="dot down" />{d.services_summary?.down}</>}
                </span>
                <span className="cell-tags">{d.tags?.slice(0, 2).map(t => <span key={t} className="mini-tag">{t}</span>)}</span>
                <span className="muted small">{timeAgo(d.last_seen_up_at)}</span>
                <span><ChevronRight size={15} className="muted" /></span>
              </div>
            ))}
            {!devices.length && !loading && <EmptyState title="Sin dispositivos" message="Crea el primer dispositivo para empezar a monitorizar." icon={<Server size={24} />} />}
          </div>
        )}
      </div>

      <Modal open={!!selected} onClose={() => { setSelected(null); setDetail(null); setSnmpOpen(false) }} title={selected?.name || 'Dispositivo'} subtitle={selected?.primary_ip} width={720}>
        {!detail ? <Spinner /> : (
          <div className="device-detail">
            <div className="device-detail-top">
              <div className={`device-hero ${selected?.status || 'unknown'}`}><Server size={26} /></div>
              <div className="device-detail-status">
                <StatusBadge status={detail.is_acknowledged && detail.status === 'down' ? 'acknowledged' : detail.status} />
                <span>{detail.status_reason}</span>
              </div>
              <div className="device-detail-actions">
                <Button variant="secondary" size="sm" onClick={() => ack(detail)}><Check size={14} /> {detail.is_acknowledged ? 'Quitar ack' : 'Reconocer'}</Button>
                <Button variant="secondary" size="sm" onClick={() => poll(detail.id)}><RefreshCcw size={14} /> Sondear</Button>
                <Button variant="secondary" size="sm" onClick={() => setToolsOpen(true)}><Wrench size={14} /> Herramientas</Button>
                <Button variant="secondary" size="sm" disabled={!detail.snmp_profile_id} onClick={() => setSnmpOpen(true)} title={detail.snmp_profile_id ? 'Inventario SNMP' : 'Sin perfil SNMP'}><Database size={14} /> SNMP</Button>
                <Button variant="secondary" size="sm" onClick={() => openEdit(detail)}><Pencil size={14} /> Editar</Button>
                <Button variant="danger" size="sm" onClick={() => void removeDevice(detail)} disabled={saving}><Trash2 size={14} /> Eliminar</Button>
              </div>
            </div>
            <div className="detail-grid">
              <KeyValue k="Dirección IP" v={detail.primary_ip} mono />
              {detail.secondary_ips?.length ? <KeyValue k="IPs secundarias" v={detail.secondary_ips.join(', ')} mono /> : null}
              <KeyValue k="Tipo" v={typeLabel(detail.device_type)} />
              <KeyValue k="MAC" v={detail.mac_address || '—'} mono />
              <KeyValue k="Última vez arriba" v={timeAgo(detail.last_seen_up_at)} />
              <KeyValue k="Cambio de estado" v={timeAgo(detail.last_status_change_at)} />
              <KeyValue k="Etiquetas" v={detail.tags?.length ? detail.tags.map(t => <span key={t} className="mini-tag">{t}</span>) : '—'} />
            </div>
            {detail.notes && <div className="notes-box">{detail.notes}</div>}
            <div className="section-label">Sondas</div>
            <div className="service-list compact">
              {detail.services.map((s: DeviceService) => (
                <div key={s.id} className="service-row static">
                  <StatusDot status={s.status} />
                  <div className="service-row-main"><b>{s.name || s.probe_name}</b><span>{s.probe_type} · cada {s.interval_sec}s</span></div>
                  <div className="service-row-value">{s.last_value ? s.last_value.toFixed(1) : '—'}<small>{s.unit}</small></div>
                </div>
              ))}
            </div>
            <div className="section-label">Objetos personalizados</div>
            <div className="custom-fields">
              {Object.entries(detail.custom_fields || {}).map(([k, v]) => <span key={k}><b>{k}</b>{String(v)}</span>)}
              {!Object.keys(detail.custom_fields || {}).length && <span className="muted">Sin campos personalizados</span>}
            </div>
            {error && <div className="form-error">{error}</div>}
          </div>
        )}
      </Modal>

      <SnmpInfoModal
        open={snmpOpen}
        onClose={() => setSnmpOpen(false)}
        deviceId={detail?.id}
        deviceName={detail?.name}
        deviceIP={detail?.primary_ip}
      />

      <ToolsModal
        open={toolsOpen}
        onClose={() => setToolsOpen(false)}
        host={detail?.primary_ip || selected?.primary_ip || ''}
        name={detail?.name || selected?.name}
      />

      <Modal open={editOpen} onClose={() => setEditOpen(false)} title="Editar dispositivo" subtitle={detail?.primary_ip} width={700} footer={<><Button variant="ghost" onClick={() => setEditOpen(false)}>Cancelar</Button><Button variant="primary" onClick={saveEdit} disabled={saving}>{saving ? 'Guardando…' : 'Guardar cambios'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={edit.name} onChange={e => setEdit({ ...edit, name: e.target.value })} /></Field>
          <Field label="IP / host"><input className="input mono" value={edit.primary_ip} onChange={e => setEdit({ ...edit, primary_ip: e.target.value })} /></Field>
          <Field label="IPs secundarias" hint="Separadas por comas."><input className="input mono" value={edit.secondary_ips} onChange={e => setEdit({ ...edit, secondary_ips: e.target.value })} /></Field>
          <Field label="DNS"><input className="input" value={edit.dns_name} onChange={e => setEdit({ ...edit, dns_name: e.target.value })} /></Field>
          <Field label="Tipo"><select className="input" value={edit.device_type} onChange={e => setEdit({ ...edit, device_type: e.target.value })}>{deviceTypes.map((t: any) => <option key={t.name} value={t.name}>{t.label}</option>)}</select></Field>
          <Field label="Perfil SNMP"><select className="input" value={edit.snmp_profile_id} onChange={e => setEdit({ ...edit, snmp_profile_id: e.target.value })}><option value="">— Sin SNMP</option>{profiles.map(p => <option key={p.id} value={p.id}>{p.name} · {p.version}</option>)}</select></Field>
          <label className="check-label"><input type="checkbox" checked={edit.refreshCustom} onChange={e => setEdit({ ...edit, refreshCustom: e.target.checked })} /> Usar intervalos SNMP propios (si no, hereda los globales)</label>
          <Field label="Refresco sistema (s)" hint="0 = solo manual"><input className="input" type="number" min={0} max={86400} disabled={!edit.refreshCustom} value={edit.sysRefresh} onChange={e => setEdit({ ...edit, sysRefresh: Number(e.target.value) })} /></Field>
          <Field label="Refresco interfaces (s)" hint="0 = solo manual"><input className="input" type="number" min={0} max={86400} disabled={!edit.refreshCustom} value={edit.ifRefresh} onChange={e => setEdit({ ...edit, ifRefresh: Number(e.target.value) })} /></Field>
          <Field label="Refresco IPs (s)" hint="0 = solo manual"><input className="input" type="number" min={0} max={86400} disabled={!edit.refreshCustom} value={edit.ipRefresh} onChange={e => setEdit({ ...edit, ipRefresh: Number(e.target.value) })} /></Field>
          <Field label="Refresco ARP (s)" hint="0 = solo manual"><input className="input" type="number" min={0} max={86400} disabled={!edit.refreshCustom} value={edit.arpRefresh} onChange={e => setEdit({ ...edit, arpRefresh: Number(e.target.value) })} /></Field>
          <Field label="Refresco rutas (s)" hint="0 = solo manual"><input className="input" type="number" min={0} max={86400} disabled={!edit.refreshCustom} value={edit.routeRefresh} onChange={e => setEdit({ ...edit, routeRefresh: Number(e.target.value) })} /></Field>
          <Field label="Agente de sondeo" hint="Central por defecto. Asigna un agente para redes remotas."><select className="input" value={edit.agent_id} onChange={e => setEdit({ ...edit, agent_id: e.target.value })}><option value="">— Servidor central</option>{agents.map(a => <option key={a.id} value={a.id}>{a.name}{a.online ? ' · online' : ' · offline'}</option>)}</select></Field>
          <Field label="Override polling (s)" hint="0 = heredar del grupo o mapa."><input className="input" type="number" min={0} value={edit.poll_interval_sec} onChange={e => setEdit({ ...edit, poll_interval_sec: Number(e.target.value) })} /></Field>
          <Field label="Override reintentos" hint="0 = heredar."><input className="input" type="number" min={0} value={edit.poll_down_retries} onChange={e => setEdit({ ...edit, poll_down_retries: Number(e.target.value) })} /></Field>
          <Field label="Override agente"><select className="input" value={edit.poll_agent_id} onChange={e => setEdit({ ...edit, poll_agent_id: e.target.value })}><option value="">— Heredar</option>{agents.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}</select></Field>
          <Field label="Override notificaciones"><select className="input" value={edit.poll_notifications} onChange={e => setEdit({ ...edit, poll_notifications: Number(e.target.value) })}><option value={-1}>Heredar</option><option value={1}>Activadas</option><option value={0}>Silenciadas</option></select></Field>
          <Field label="Etiquetas" hint="Separadas por comas."><input className="input" value={edit.tags} onChange={e => setEdit({ ...edit, tags: e.target.value })} /></Field>
        </div>
        <Field label="Notas"><textarea className="input textarea" rows={3} value={edit.notes} onChange={e => setEdit({ ...edit, notes: e.target.value })} /></Field>
        <Field label="Dependencias (Parents)" hint="Si todos los padres caen, este equipo pasa a Inalcanzable y no genera alertas propias.">
          <div className="parent-picker">
            {allDevices.map(d => (
              <label key={d.id} className={`parent-option ${edit.parent_ids.includes(d.id) ? 'active' : ''}`}>
                <input type="checkbox" checked={edit.parent_ids.includes(d.id)} onChange={e => setEdit({ ...edit, parent_ids: e.target.checked ? [...edit.parent_ids, d.id] : edit.parent_ids.filter(id => id !== d.id) })} />
                <StatusDot status={d.status} />
                <span>{d.name}</span>
                <small className="mono">{d.primary_ip}</small>
              </label>
            ))}
            {!allDevices.length && <span className="muted small">No hay otros dispositivos.</span>}
          </div>
        </Field>
        <label className="check-label"><input type="checkbox" checked={edit.monitoring} onChange={e => setEdit({ ...edit, monitoring: e.target.checked })} /> Monitorización activa</label>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={createOpen} onClose={() => setCreateOpen(false)} title="Nuevo dispositivo" subtitle="Se creará con una sonda ICMP por defecto." footer={<><Button variant="ghost" onClick={() => setCreateOpen(false)}>Cancelar</Button><Button variant="primary" onClick={create} disabled={saving}>{saving ? 'Creando…' : 'Crear dispositivo'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={name} onChange={e => setName(e.target.value)} placeholder="Switch-Rack-01" autoFocus /></Field>
          <Field label="IP / host"><input className="input mono" value={ip} onChange={e => setIp(e.target.value)} placeholder="10.0.0.10" /></Field>
          <Field label="IPs secundarias" hint="Opcional, separadas por comas."><input className="input mono" value={secondary} onChange={e => setSecondary(e.target.value)} placeholder="10.0.0.11" /></Field>
          <Field label="Tipo"><select className="input" value={type} onChange={e => setType(e.target.value)}>{deviceTypes.map((t: any) => <option key={t.name} value={t.name}>{t.label}</option>)}</select></Field>
          <Field label="Perfil SNMP"><select className="input" value={profile} onChange={e => setProfile(e.target.value)}><option value="">— Sin SNMP</option>{profiles.map(p => <option key={p.id} value={p.id}>{p.name} · {p.version}</option>)}</select></Field>
        </div>
        {error && <div className="form-error">{error}</div>}
      </Modal>
    </div>
  )
}
