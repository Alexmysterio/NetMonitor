import { useEffect, useMemo, useState } from 'react'
import { Check, Loader2, MapPin, Radar, Server, Sparkles } from 'lucide-react'
import { apiGet, apiSend } from '../api'
import type { SnmpProfile } from '../types'
import { Button, EmptyState, Field, Modal } from '../ui'

interface Found {
  ip: string
  name: string
  sys_descr: string
  sys_object_id: string
  latency_ms: number
  existing_device_id: string
}

export default function DiscoveryModal({
  open,
  onClose,
  mapId,
  position,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  mapId: string
  position: { x: number; y: number }
  onCreated: () => void
}) {
  const [cidr, setCidr] = useState('192.168.1.0/24')
  const [profiles, setProfiles] = useState<SnmpProfile[]>([])
  const [profile, setProfile] = useState('')
  const [scanning, setScanning] = useState(false)
  const [results, setResults] = useState<Found[] | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [adding, setAdding] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setError('')
    setResults(null)
    setSelected(new Set())
    apiGet<SnmpProfile[]>('/snmp-profiles').then(p => {
      setProfiles(p)
      const def = p.find(x => x.is_default) || p[0]
      if (def) setProfile(def.id)
    }).catch(() => {})
  }, [open])

  const scan = async () => {
    setScanning(true)
    setError('')
    setResults(null)
    try {
      const found = await apiSend<Found[]>('/discovery/scan', 'POST', { cidr, snmp_profile_id: profile || null })
      setResults(found)
      setSelected(new Set(found.map(f => f.ip)))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setScanning(false)
    }
  }

  const addSelected = async () => {
    if (!results) return
    setAdding(true)
    setError('')
    try {
      const chosen = results.filter(r => selected.has(r.ip))
      let dx = 0
      for (const r of chosen) {
        let deviceId = r.existing_device_id
        if (!deviceId) {
          const created = await apiSend<{ id: string }>('/devices', 'POST', {
            name: r.name || `Host-${r.ip.replaceAll('.', '-')}`,
            primary_ip: r.ip,
            device_type: r.sys_descr?.toLowerCase().includes('routeros') ? 'router' : 'generic',
            snmp_profile_id: profile || null,
            custom_fields: { discovered_sysdescr: r.sys_descr, discovered_sys_object_id: r.sys_object_id },
            create_default_ping: true,
          })
          deviceId = created.id
        }
        await apiSend(`/maps/${mapId}/elements`, 'POST', {
          element_type: 'device',
          device_id: deviceId,
          pos_x: position.x + (dx % 4) * 210,
          pos_y: position.y + Math.floor(dx / 4) * 120,
          width: 190,
          height: 96,
          shape: 'rounded_rect',
          icon_name: 'server',
          label_template: '[Device.Name]\n[Device.FirstAddress]',
        })
        dx++
      }
      onCreated()
      onClose()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setAdding(false)
    }
  }

  const toggle = (ip: string) => {
    const next = new Set(selected)
    if (next.has(ip)) next.delete(ip)
    else next.add(ip)
    setSelected(next)
  }

  const selectedCount = useMemo(() => results?.filter(r => selected.has(r.ip)).length || 0, [results, selected])

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Descubrir equipos"
      subtitle="Barrido ICMP + identificación SNMP. Los equipos existentes se reutilizan."
      width={720}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Cancelar</Button>
          <Button variant="primary" onClick={addSelected} disabled={adding || !selectedCount}>
            <MapPin size={15} /> {adding ? 'Añadiendo…' : `Añadir ${selectedCount || ''} al mapa`}
          </Button>
        </>
      }
    >
      <div className="discovery-bar">
        <Field label="Rango CIDR" hint="Máximo /24 recomendado. Se excluyen red y broadcast.">
          <input className="input mono" value={cidr} onChange={e => setCidr(e.target.value)} placeholder="10.20.1.0/24" />
        </Field>
        <Field label="Perfil SNMP (opcional)">
          <select className="input" value={profile} onChange={e => setProfile(e.target.value)}>
            <option value="">Solo ICMP</option>
            {profiles.map(p => <option key={p.id} value={p.id}>{p.name} · {p.version}</option>)}
          </select>
        </Field>
        <Button variant="dark" className="discovery-scan-btn" onClick={scan} disabled={scanning}>
          {scanning ? <Loader2 size={15} className="spin" /> : <Radar size={15} />} {scanning ? 'Escaneando…' : 'Escanear'}
        </Button>
      </div>

      {scanning && (
        <div className="discovery-progress">
          <div className="discovery-radar"><Radar size={22} /></div>
          <div><b>Barriendo {cidr}</b><span>ICMP en paralelo · identificando sysName/sysDescr…</span></div>
        </div>
      )}

      {results && (
        <div className="discovery-results">
          <div className="discovery-summary"><Sparkles size={14} /> {results.length} equipos respondieron</div>
          <div className="discovery-list">
            {results.map(r => (
              <button key={r.ip} className={`discovery-row ${selected.has(r.ip) ? 'selected' : ''}`} onClick={() => toggle(r.ip)}>
                <span className={`discovery-check ${selected.has(r.ip) ? 'on' : ''}`}>{selected.has(r.ip) && <Check size={11} />}</span>
                <span className="discovery-icon"><Server size={15} /></span>
                <span className="discovery-info">
                  <b>{r.name || `Host ${r.ip}`}</b>
                  <small>{r.sys_descr || 'Sin SNMP · solo ICMP'} {r.sys_object_id ? `· ${r.sys_object_id}` : ''}</small>
                </span>
                <span className="mono discovery-ip">{r.ip}</span>
                <span className="discovery-latency">{r.latency_ms.toFixed(1)} ms</span>
                {r.existing_device_id && <span className="existing-pill">ya existe</span>}
              </button>
            ))}
            {!results.length && <EmptyState title="Sin respuestas" message="No se ha encontrado ningún equipo activo en ese rango." icon={<Radar size={24} />} />}
          </div>
        </div>
      )}

      {error && <div className="form-error">{error}</div>}
    </Modal>
  )
}
