import { useEffect, useMemo, useState } from 'react'
import { Boxes, Building2, Check, Circle, Diamond, Frame, Hexagon, Network, Plus, Search, Server, Square, Type } from 'lucide-react'
import { apiGet, apiSend } from '../api'
import type { Device, MapInfo, Shape, SnmpProfile } from '../types'
import { Button, deviceTypeLabel, EmptyState, Field, Modal, StatusDot } from '../ui'

const SHAPES: { id: Shape; label: string; icon: React.ReactNode }[] = [
  { id: 'rounded_rect', label: 'Rectángulo', icon: <Square size={15} /> },
  { id: 'circle', label: 'Círculo', icon: <Circle size={15} /> },
  { id: 'square', label: 'Cuadrado', icon: <Square size={15} /> },
  { id: 'pill', label: 'Cápsula', icon: <span className="shape-pill-icon" /> },
  { id: 'diamond', label: 'Rombo', icon: <Diamond size={15} /> },
  { id: 'hexagon', label: 'Hexágono', icon: <Hexagon size={15} /> },
]

export default function AddNodeModal({
  open,
  onClose,
  mapId,
  currentMapId,
  position,
  defaultShape,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  mapId: string
  currentMapId: string
  position: { x: number; y: number }
  defaultShape?: Shape
  onCreated: () => void
}) {
  const [tab, setTab] = useState<'device' | 'new' | 'submap' | 'network' | 'zone' | 'text'>('device')
  const [devices, setDevices] = useState<Device[]>([])
  const [maps, setMaps] = useState<MapInfo[]>([])
  const [profiles, setProfiles] = useState<SnmpProfile[]>([])
  const [deviceTypes, setDeviceTypes] = useState<any[]>([])
  const [q, setQ] = useState('')
  const [selectedDevice, setSelectedDevice] = useState('')
  const [selectedSubmap, setSelectedSubmap] = useState('')
  const [networkCIDR, setNetworkCIDR] = useState('')
  const [networkLabel, setNetworkLabel] = useState('')
  const [shape, setShape] = useState<Shape>(defaultShape || 'circle')
  const [shapeTouched, setShapeTouched] = useState(false)
  const [name, setName] = useState('')
  const [ip, setIp] = useState('')
  const [type, setType] = useState('router')
  const [profile, setProfile] = useState('')
  const [zoneLabel, setZoneLabel] = useState('Zona')
  const [zoneColor, setZoneColor] = useState('#94a3b8')
  const [zoneFill, setZoneFill] = useState('#f8fafc')
  const [textLabel, setTextLabel] = useState('Texto del mapa')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setError('')
    setShape(defaultShape || 'circle')
    setShapeTouched(false)
    void Promise.all([
      apiGet<Device[]>('/devices'),
      apiGet<MapInfo[]>('/maps'),
      apiGet<SnmpProfile[]>('/snmp-profiles'),
      apiGet<any[]>('/device-types'),
    ]).then(([d, m, p, t]) => {
      setDevices(d)
      setMaps(m.filter(x => x.id !== currentMapId))
      setProfiles(p)
      setDeviceTypes(t)
      if (p[0]) setProfile(p[0].id)
    })
  }, [open, currentMapId])

  const filtered = useMemo(
    () => devices.filter(d => !q || d.name.toLowerCase().includes(q.toLowerCase()) || d.primary_ip.includes(q)),
    [devices, q],
  )

  const updateType = (value: string) => {
    setType(value)
    const meta = deviceTypes.find((t: any) => t.name === value)
    if (meta?.shape) {
      setShape(meta.shape)
      setShapeTouched(false)
    }
  }

  const pickDevice = (id: string) => {
    setSelectedDevice(id)
    if (!shapeTouched) {
      const d = devices.find(x => x.id === id)
      const meta = deviceTypes.find((t: any) => t.name === (d?.device_type || 'generic'))
      if (meta?.shape) setShape(meta.shape)
    }
  }

  const createElement = async (payload: Record<string, unknown>) => {
    await apiSend('/maps/' + mapId + '/elements', 'POST', {
      element_type: 'device',
      pos_x: position.x,
      pos_y: position.y,
      width: 190,
      height: 96,
      shape,
      icon_name: type,
      ...payload,
    })
    onCreated()
    onClose()
  }

  const submit = async () => {
    setError('')
    setSaving(true)
    try {
      if (tab === 'device') {
        if (!selectedDevice) throw new Error('Selecciona un dispositivo')
        const d = devices.find(x => x.id === selectedDevice)
        const meta = deviceTypes.find((t: any) => t.name === (d?.device_type || 'generic'))
        await createElement({ device_id: selectedDevice, icon_name: meta?.icon || d?.device_type || 'server', shape, custom_style: meta?.color ? { color: meta.color } : {} })
      } else if (tab === 'submap') {
        if (!selectedSubmap) throw new Error('Selecciona un mapa')
        const m = maps.find(x => x.id === selectedSubmap)
        await apiSend('/maps/' + mapId + '/elements', 'POST', {
          element_type: 'submap',
          target_submap_id: selectedSubmap,
          pos_x: position.x,
          pos_y: position.y,
          width: 220,
          height: 120,
          shape,
          icon_name: 'building',
          label_template: `${m?.name || 'Submapa'}\n[Submap.Summary]`,
        })
        onCreated()
        onClose()
      } else if (tab === 'network') {
        const cidr = networkCIDR.trim()
        if (!/^[0-9a-fA-F:.]+\/\d{1,3}$/.test(cidr)) throw new Error('Introduce una subred válida, por ejemplo 192.0.2.0/24')
        await apiSend('/maps/' + mapId + '/elements', 'POST', {
          element_type: 'network',
          network_cidr: cidr,
          pos_x: position.x,
          pos_y: position.y,
          width: 240,
          height: 120,
          shape,
          icon_name: 'network',
          label_template: networkLabel.trim() || '[Network.CIDR]\n[Network.Summary]',
          custom_style: { color: '#0f766e', fill: '#ecfdf5' },
        })
        onCreated()
        onClose()
      } else if (tab === 'zone') {
        await apiSend('/maps/' + mapId + '/elements', 'POST', {
          element_type: 'zone',
          pos_x: position.x,
          pos_y: position.y,
          width: 320,
          height: 180,
          shape: 'rounded_rect',
          show_icon: false,
          z_index: 0,
          label_template: zoneLabel || 'Zona',
          custom_style: { color: zoneColor, fill: zoneFill, dashed: true },
        })
        onCreated()
        onClose()
      } else if (tab === 'text') {
        await apiSend('/maps/' + mapId + '/elements', 'POST', {
          element_type: 'text_label',
          pos_x: position.x,
          pos_y: position.y,
          width: 260,
          height: 60,
          shape: 'rounded_rect',
          show_icon: false,
          z_index: 20,
          label_template: textLabel || 'Texto',
        })
        onCreated()
        onClose()
      } else {
        if (!name.trim() || !ip.trim()) throw new Error('Nombre e IP son obligatorios')
        const data = await apiSend<{ id: string }>('/devices', 'POST', {
          name,
          primary_ip: ip,
          device_type: type,
          snmp_profile_id: profile || null,
          create_default_ping: true,
        })
        const meta = deviceTypes.find((t: any) => t.name === type)
        await createElement({ device_id: data.id, icon_name: meta?.icon || type, custom_style: meta?.color ? { color: meta.color } : {} })
      }
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Añadir al mapa"
      subtitle="Incorpora un equipo existente, crea uno nuevo o enlaza un submapa."
      width={700}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Cancelar</Button>
          <Button variant="primary" onClick={submit} disabled={saving}>
            <Plus size={16} /> {saving ? 'Añadiendo…' : 'Añadir al mapa'}
          </Button>
        </>
      }
    >
      <div className="segmented">
        <button className={tab === 'device' ? 'active' : ''} onClick={() => setTab('device')}><Boxes size={15} /> Dispositivo</button>
        <button className={tab === 'new' ? 'active' : ''} onClick={() => setTab('new')}><Server size={15} /> Nuevo equipo</button>
        <button className={tab === 'submap' ? 'active' : ''} onClick={() => setTab('submap')}><Building2 size={15} /> Submapa</button>
        <button className={tab === 'network' ? 'active' : ''} onClick={() => setTab('network')}><Network size={15} /> Red</button>
        <button className={tab === 'zone' ? 'active' : ''} onClick={() => setTab('zone')}><Frame size={15} /> Zona</button>
        <button className={tab === 'text' ? 'active' : ''} onClick={() => setTab('text')}><Type size={15} /> Texto</button>
      </div>

      {tab === 'device' && (
        <div className="add-node-body">
          <div className="search-inline">
            <Search size={15} />
            <input className="input" value={q} onChange={e => setQ(e.target.value)} placeholder="Filtrar dispositivos…" />
          </div>
          <div className="device-pick-list">
            {filtered.map(d => (
              <button key={d.id} className={`device-pick ${selectedDevice === d.id ? 'selected' : ''}`} onClick={() => pickDevice(d.id)}>
                <StatusDot status={d.status} />
                <div>
                  <b>{d.name}</b>
                  <span className="mono">{d.primary_ip}</span>
                </div>
                <span className="pick-type">{deviceTypeLabel(d.device_type)}</span>
                {selectedDevice === d.id && <Check size={15} className="pick-check" />}
              </button>
            ))}
            {filtered.length === 0 && <EmptyState title="Sin equipos" message="No hay dispositivos que coincidan. Crea uno nuevo." icon={<Boxes size={22} />} />}
          </div>
        </div>
      )}

      {tab === 'new' && (
        <div className="form-grid two">
          <Field label="Nombre del equipo">
            <input className="input" value={name} onChange={e => setName(e.target.value)} placeholder="Ej. Switch-Rack-04" autoFocus />
          </Field>
          <Field label="Dirección IP / host">
            <input className="input mono" value={ip} onChange={e => setIp(e.target.value)} placeholder="10.20.1.24" />
          </Field>
          <Field label="Tipo de equipo">
            <select className="input" value={type} onChange={e => updateType(e.target.value)}>
              {deviceTypes.map((t: any) => <option key={t.name} value={t.name}>{t.label}</option>)}
            </select>
          </Field>
          <Field label="Perfil SNMP" hint="Opcional. Se usará para sondas SNMP y etiquetas dinámicas.">
            <select className="input" value={profile} onChange={e => setProfile(e.target.value)}>
              <option value="">— Sin SNMP</option>
              {profiles.map(p => <option key={p.id} value={p.id}>{p.name} · {p.version}</option>)}
            </select>
          </Field>
        </div>
      )}

      {tab === 'submap' && (
        <div className="device-pick-list">
          {maps.map(m => (
            <button key={m.id} className={`device-pick ${selectedSubmap === m.id ? 'selected' : ''}`} onClick={() => setSelectedSubmap(m.id)}>
              <Building2 size={17} />
              <div><b>{m.name}</b><span>{m.counts.up + m.counts.warning + m.counts.down} dispositivos</span></div>
              {selectedSubmap === m.id && <Check size={15} className="pick-check" />}
            </button>
          ))}
          {maps.length === 0 && <EmptyState title="Sin submapas disponibles" message="Crea primero otro mapa desde el panel izquierdo." icon={<Building2 size={22} />} />}
        </div>
      )}

      {tab === 'network' && (
        <div className="form-grid two">
          <Field label="Subred / CIDR" hint="El estado se agrega con los dispositivos contenidos en esta red.">
            <input className="input mono" value={networkCIDR} onChange={e => setNetworkCIDR(e.target.value)} placeholder="192.0.2.0/24" autoFocus />
          </Field>
          <Field label="Etiqueta" hint="Opcional. Admite [Network.CIDR], [Network.Summary] y contadores.">
            <input className="input" value={networkLabel} onChange={e => setNetworkLabel(e.target.value)} placeholder="[Network.CIDR]\n[Network.Summary]" />
          </Field>
        </div>
      )}

      {tab === 'zone' && (
        <div className="form-grid two">
          <Field label="Nombre de la zona"><input className="input" value={zoneLabel} onChange={e => setZoneLabel(e.target.value)} placeholder="CPD Central · Rack A" autoFocus /></Field>
          <div />
          <Field label="Color del borde"><input className="input color-input" type="color" value={zoneColor} onChange={e => setZoneColor(e.target.value)} /></Field>
          <Field label="Color de relleno"><input className="input color-input" type="color" value={zoneFill} onChange={e => setZoneFill(e.target.value)} /></Field>
        </div>
      )}

      {tab === 'text' && (
        <Field label="Texto"><textarea className="input textarea" rows={3} value={textLabel} onChange={e => setTextLabel(e.target.value)} autoFocus /></Field>
      )}

      {tab !== 'zone' && tab !== 'text' && (
        <div className="shape-picker">
          <span className="field-label">Forma del nodo</span>
          <div className="shape-options">
            {SHAPES.map(s => (
              <button key={s.id} className={`shape-option ${shape === s.id ? 'active' : ''}`} onClick={() => { setShape(s.id); setShapeTouched(true) }} title={s.label}>
                {s.icon}
                <span>{s.label}</span>
              </button>
            ))}
          </div>
        </div>
      )}

      {error && <div className="form-error">{error}</div>}
    </Modal>
  )
}
