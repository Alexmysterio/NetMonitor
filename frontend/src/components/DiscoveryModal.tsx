import { useEffect, useMemo, useState } from 'react'
import { Check, Loader2, MapPin, Radar, Server, Sparkles } from 'lucide-react'
import { apiGet, apiSend } from '../api'
import { confirmAction } from '../confirm'
import type { SnmpProfile } from '../types'
import { Button, EmptyState, Field, Modal } from '../ui'

interface Found {
  ip: string
  name: string
  sys_descr: string
  sys_object_id: string
  latency_ms: number
  existing_device_id: string
  existing_device_name: string
  existing_device_ip: string
  ip_mismatch: boolean
  method: string
  snmp_profile_id: string
  tcp_port: number
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
  const [selectedProfiles, setSelectedProfiles] = useState<string[]>([])
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
    setSelectedProfiles([])
    apiGet<SnmpProfile[]>('/snmp-profiles').then(p => {
      setProfiles(p)
      const def = p.find(x => x.is_default) || p[0]
      if (def) setSelectedProfiles([def.id])
    }).catch(() => {})
  }, [open])

  const scan = async () => {
    setScanning(true)
    setError('')
    setResults(null)
    try {
      const found = await apiSend<Found[]>('/discovery/scan', 'POST', {
        cidr,
        snmp_profile_ids: selectedProfiles,
      })
      const mismatches = found.filter(f => f.ip_mismatch)
      if (mismatches.length) {
        const detail = mismatches
          .map(f => `${f.existing_device_name || f.name}: ${f.existing_device_ip} → ${f.ip}`)
          .join('\n')
        const update = await confirmAction(
          `Estos equipos ya existen en el inventario con otra IP:\n\n${detail}\n\n¿Quieres actualizar su IP en lugar de crear duplicados?`,
          { title: 'Equipos ya inventariados', confirmLabel: 'Actualizar IPs', danger: false },
        )
        if (update) {
          for (const item of mismatches) {
            try {
              await apiSend(`/devices/${item.existing_device_id}`, 'PUT', { primary_ip: item.ip })
              item.existing_device_ip = item.ip
              item.ip_mismatch = false
            } catch (e) {
              setError(`No se pudo actualizar ${item.existing_device_name || item.name}: ${(e as Error).message}`)
            }
          }
        }
      }
      const visible = found.filter(f => !f.ip_mismatch)
      setResults(visible)
      setSelected(new Set(visible.map(f => f.ip)))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setScanning(false)
    }
  }

  const toggleProfile = (id: string) => {
    setError('')
    if (selectedProfiles.includes(id)) {
      setSelectedProfiles(selectedProfiles.filter(p => p !== id))
      return
    }
    if (selectedProfiles.length >= 3) {
      setError('Máximo 3 perfiles SNMP por descubrimiento')
      return
    }
    setSelectedProfiles([...selectedProfiles, id])
  }

  const addSelected = async () => {
    if (!results) return
    setAdding(true)
    setError('')
    try {
      const chosen = results.filter(r => selected.has(r.ip))
      const canvas = await apiGet<{ elements: { element_type: string; device_id: string }[] }>(`/maps/${mapId}/canvas`)
      const onMap = new Set(canvas.elements.filter(e => e.element_type === 'device' && e.device_id).map(e => e.device_id))
      let dx = 0
      for (const r of chosen) {
        const method = r.method || 'icmp'
        const profile = r.snmp_profile_id || selectedProfiles[0] || ''
        let deviceId = r.existing_device_id
        if (!deviceId) {
          const created = await apiSend<{ id: string }>('/devices', 'POST', {
            name: r.name || `Host-${r.ip.replaceAll('.', '-')}`,
            primary_ip: r.ip,
            device_type: r.sys_descr?.toLowerCase().includes('routeros') ? 'router' : 'generic',
            snmp_profile_id: profile || null,
            custom_fields: { discovered_sysdescr: r.sys_descr, discovered_sys_object_id: r.sys_object_id },
            create_default_ping: method === 'icmp',
          })
          deviceId = created.id
        }
        // A host discovered through TCP/SNMP usually blocks ICMP. Create the
        // probe that actually works instead of a Ping that would stay down.
        try {
          await apiSend(`/devices/${deviceId}/ensure-probe`, 'POST', {
            method,
            tcp_port: r.tcp_port || 0,
            snmp_profile_id: profile,
          })
        } catch {
          // Adding the device to the map must not fail if the probe could not
          // be adjusted (for example, a restricted user or a missing profile).
        }
        if (!onMap.has(deviceId)) {
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
          onMap.add(deviceId)
          dx++
        }
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
        <Field label="Rangos CIDR" hint="Puedes separar varios rangos con comas. Máximo 65.536 direcciones por barrido; para redes mayores, divide en segmentos.">
          <input className="input mono" value={cidr} onChange={e => setCidr(e.target.value)} placeholder="10.20.0.0/16, 192.168.1.0/24" />
        </Field>
        <Field label="Perfiles SNMP (máx. 3)" hint="Se prueban en orden. Si el equipo no responde a ICMP, se intenta SNMP y puertos comunes.">
          <div className="profile-checks">
            {!profiles.length && <span className="muted small">Sin perfiles SNMP</span>}
            {profiles.map(p => (
              <label key={p.id} className={`profile-check ${selectedProfiles.includes(p.id) ? 'active' : ''}`}>
                <input type="checkbox" checked={selectedProfiles.includes(p.id)} onChange={() => toggleProfile(p.id)} />
                <span>{p.name} · {p.version}</span>
              </label>
            ))}
          </div>
        </Field>
        <Button variant="dark" className="discovery-scan-btn" onClick={scan} disabled={scanning}>
          {scanning ? <Loader2 size={15} className="spin" /> : <Radar size={15} />} {scanning ? 'Escaneando…' : 'Escanear'}
        </Button>
      </div>

      {scanning && (
        <div className="discovery-progress">
          <div className="discovery-radar"><Radar size={22} /></div>
          <div><b>Barriendo {cidr}</b><span>ICMP en paralelo · identificando sysName/sysDescr. Los rangos grandes pueden tardar varios minutos.</span></div>
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
                  <small>{r.sys_descr || (r.method === 'tcp' ? 'Detectado por TCP' : r.method === 'snmp' ? 'Detectado por SNMP' : 'Sin SNMP · solo ICMP')} {r.sys_object_id ? `· ${r.sys_object_id}` : ''}</small>
                </span>
                <span className="mono discovery-ip">{r.ip}</span>
                <span className={`discovery-method ${r.method || 'icmp'}`}>
                  {r.method === 'tcp' ? `TCP ${r.tcp_port || ''}`.trim() : r.method === 'snmp' ? 'SNMP' : 'ICMP'}
                </span>
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
