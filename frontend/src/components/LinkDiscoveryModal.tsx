import { useEffect, useMemo, useState } from 'react'
import { Cable, Check, Loader2, Network, Sparkles } from 'lucide-react'
import { apiGet, apiSend } from '../api'
import type { CanvasData } from '../types'
import { Button, EmptyState, Modal } from '../ui'

interface Suggestion {
  source_device_id: string
  target_device_id: string
  source_name: string
  target_name: string
  local_port: string
  remote_port: string
  local_if_index?: number
  remote_if_index?: number
  stp_state?: string
  vlan?: number
  protocol: string
}

export default function LinkDiscoveryModal({
  open,
  onClose,
  mapId,
  onCreated,
}: {
  open: boolean
  onClose: () => void
  mapId: string
  onCreated: () => void
}) {
  const [canvas, setCanvas] = useState<CanvasData | null>(null)
  const [suggestions, setSuggestions] = useState<Suggestion[] | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [scanning, setScanning] = useState(false)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setSuggestions(null)
    setSelected(new Set())
    setError('')
    apiGet<CanvasData>(`/maps/${mapId}/canvas`).then(setCanvas).catch(() => setCanvas(null))
  }, [open, mapId])

  const keyOf = (s: Suggestion) => `${s.source_device_id}|${s.target_device_id}`
  const existing = useMemo(() => {
    const set = new Set<string>()
    if (!canvas) return set
    const byDevice = new Map(canvas.elements.filter(e => e.device_id).map(e => [e.device_id, e.id]))
    canvas.links.forEach(l => {
      const src = canvas.elements.find(e => e.id === l.source_element_id)?.device_id
      const tgt = canvas.elements.find(e => e.id === l.target_element_id)?.device_id
      if (src && tgt) {
        set.add(`${src}|${tgt}`)
        set.add(`${tgt}|${src}`)
      }
    })
    return set
  }, [canvas])

  const scan = async () => {
    setScanning(true)
    setError('')
    try {
      const found = await apiSend<Suggestion[]>('/discovery/links', 'POST', { map_id: mapId })
      setSuggestions(found)
      setSelected(new Set(found.map(keyOf).filter(k => !existing.has(k))))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setScanning(false)
    }
  }

  const create = async () => {
    if (!canvas || !suggestions) return
    setCreating(true)
    setError('')
    try {
      const byDevice = new Map(canvas.elements.filter(e => e.device_id).map(e => [e.device_id, e.id]))
      for (const s of suggestions) {
        if (!selected.has(keyOf(s))) continue
        const source = byDevice.get(s.source_device_id)
        const target = byDevice.get(s.target_device_id)
        if (!source || !target) continue
        await apiSend(`/maps/${mapId}/links`, 'POST', {
          source_element_id: source,
          target_element_id: target,
          source_handle: 'center',
          target_handle: 'center',
          link_type: 'ethernet',
          max_speed_bps: 1_000_000_000,
          label_template: '',
        })
      }
      onCreated()
      onClose()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setCreating(false)
    }
  }

  const toggle = (s: Suggestion) => {
    const k = keyOf(s)
    const next = new Set(selected)
    if (next.has(k)) next.delete(k)
    else next.add(k)
    setSelected(next)
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Descubrir enlaces L2"
      subtitle="Lee LLDP, CDP, MNDP, FDB, Q-BRIDGE y ARP y propone enlaces entre vecinos ya inventariados."
      width={680}
      footer={<><Button variant="ghost" onClick={onClose}>Cancelar</Button><Button variant="primary" onClick={create} disabled={creating || !selected.size}><Cable size={15} /> {creating ? 'Creando…' : `Crear ${selected.size || ''} enlaces`}</Button></>}
    >
      <div className="discovery-l2-head">
        <div><b>Topología descubierta</b><span>Se emparejan vecinos LLDP/CDP/MNDP, FDB/Q-BRIDGE y ARP con dispositivos del mapa. No se crean equipos nuevos.</span></div>
        <Button variant="dark" size="sm" onClick={scan} disabled={scanning}>{scanning ? <Loader2 size={14} className="spin" /> : <Network size={14} />} {scanning ? 'Consultando…' : 'Escanear L2'}</Button>
      </div>

      {suggestions && (
        <div className="discovery-results">
          <div className="discovery-summary"><Sparkles size={14} /> {suggestions.length} vecinos detectados</div>
          <div className="discovery-list">
            {suggestions.map(s => {
              const already = existing.has(keyOf(s))
              return (
                <button key={keyOf(s)} className={`discovery-row ${selected.has(keyOf(s)) ? 'selected' : ''}`} onClick={() => !already && toggle(s)}>
                  <span className={`discovery-check ${selected.has(keyOf(s)) ? 'on' : ''}`}>{selected.has(keyOf(s)) && <Check size={11} />}</span>
                  <span className="discovery-icon"><Network size={15} /></span>
                  <span className="discovery-info"><b>{s.source_name} ↔ {s.target_name}</b><small>{s.protocol}{s.vlan ? ` VLAN ${s.vlan}` : ''} · local {s.local_port || '—'}{s.local_if_index ? ` (if${s.local_if_index})` : ''} · remoto {s.remote_port || '—'}{s.remote_if_index ? ` (if${s.remote_if_index})` : ''}{s.stp_state ? ` · STP ${s.stp_state}` : ''}</small></span>
                  {already ? <span className="existing-pill">ya existe</span> : <span className="builtin-pill">sugerido</span>}
                </button>
              )
            })}
            {!suggestions.length && <EmptyState title="Sin vecinos detectados" message="Verifica perfiles SNMP, MACs inventariadas y que LLDP/CDP/MNDP estén activos en los equipos." icon={<Network size={24} />} />}
          </div>
        </div>
      )}
      {error && <div className="form-error">{error}</div>}
    </Modal>
  )
}
