import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Activity, AlertTriangle, CheckCircle2, Database, FileJson, KeyRound, Map as MapIcon, RefreshCw, Server, Upload } from 'lucide-react'
import { apiGet, apiSend, apiUpload } from '../api'
import type { DudeImport, DudePreview, MapInfo, Probe, SnmpProfile } from '../types'
import { Button, EmptyState, Field, Modal, Spinner, StatusBadge, timeAgo } from '../ui'

export default function ImportsPage() {
  const [history, setHistory] = useState<DudeImport[]>([])
  const [maps, setMaps] = useState<MapInfo[]>([])
  const [snmpProfiles, setSnmpProfiles] = useState<SnmpProfile[]>([])
  const [snmpAssignments, setSnmpAssignments] = useState<Record<string, string>>({})
  const [bulkProfile, setBulkProfile] = useState('')
  const [probes, setProbes] = useState<Probe[]>([])
  const [selectedProbes, setSelectedProbes] = useState<string[]>([])
  const [deviceTypes, setDeviceTypes] = useState<{ name: string; label: string }[]>([])
  const [typeAssignments, setTypeAssignments] = useState<Record<string, string>>({})
  const [bulkType, setBulkType] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [previewOpen, setPreviewOpen] = useState(false)
  const [importID, setImportID] = useState('')
  const [preview, setPreview] = useState<DudePreview | null>(null)
  const [selectedMaps, setSelectedMaps] = useState<string[]>([])
  const [selectedDevices, setSelectedDevices] = useState<string[]>([])
  const [parentMap, setParentMap] = useState('')
  const [prefix, setPrefix] = useState('')
  const [createLinks, setCreateLinks] = useState(true)
  const [result, setResult] = useState<Record<string, unknown> | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()

  const load = async () => {
    setLoading(true)
    try {
      const [h, m, p, pr, dt] = await Promise.all([
        apiGet<DudeImport[]>('/imports/dude'),
        apiGet<MapInfo[]>('/maps'),
        apiGet<SnmpProfile[]>('/snmp-profiles'),
        apiGet<Probe[]>('/probes'),
        apiGet<{ name: string; label: string }[]>('/device-types'),
      ])
      setHistory(h); setMaps(m); setSnmpProfiles(p); setProbes(pr); setDeviceTypes(dt)
    } catch (e) { setError((e as Error).message) } finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

  const flash = (msg: string) => { setNotice(msg); window.setTimeout(() => setNotice(''), 5000) }

  const defaultProbeSelection = () => {
    const ping = probes.find(p => p.probe_type === 'icmp' && p.name.toLowerCase() === 'ping')
      || probes.find(p => p.probe_type === 'icmp')
    return ping ? [ping.id] : []
  }

  const defaultTypeAssignments = (list: { source_id: string; device_type: string }[]) => {
    const next: Record<string, string> = {}
    for (const device of list) next[device.source_id] = device.device_type || 'generic'
    return next
  }

  const upload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    setBusy(true); setError(''); setResult(null)
    try {
      const form = new FormData()
      form.append('file', file)
      const data = await apiUpload<{ id: string; preview: DudePreview }>('/imports/dude/preview', form)
      setImportID(data.id)
      setPreview(data.preview)
      setSelectedMaps(data.preview.maps.map(m => m.source_id))
      setSelectedDevices(data.preview.devices.map(d => d.source_id))
      setSnmpAssignments({})
      setBulkProfile('')
      setSelectedProbes(defaultProbeSelection())
      setTypeAssignments(defaultTypeAssignments(data.preview.devices))
      setBulkType('')
      setParentMap('')
      setPrefix('')
      setCreateLinks(true)
      setPreviewOpen(true)
      await load()
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }

  const openHistory = async (item: DudeImport) => {
    setBusy(true); setError(''); setResult(null)
    try {
      const full = await apiGet<DudeImport>(`/imports/dude/${item.id}`)
      if (!full.preview) throw new Error('Esta importación no conserva el preview')
      setImportID(full.id)
      setPreview(full.preview)
      setSelectedMaps(full.preview.maps.map(m => m.source_id))
      setSelectedDevices(full.preview.devices.map(d => d.source_id))
      setSnmpAssignments({})
      setBulkProfile('')
      setSelectedProbes(defaultProbeSelection())
      setTypeAssignments(defaultTypeAssignments(full.preview.devices))
      setBulkType('')
      setParentMap('')
      setPrefix('')
      setCreateLinks(true)
      setPreviewOpen(true)
      setResult((full.result as Record<string, unknown>) || null)
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }

  const assignSNMP = (sourceID: string, profileID: string) => {
    setSnmpAssignments(prev => {
      const next = { ...prev }
      if (profileID) next[sourceID] = profileID
      else delete next[sourceID]
      return next
    })
  }

  const applySNMPToAll = () => {
    if (!preview) return
    setSnmpAssignments(prev => {
      const next = { ...prev }
      for (const device of preview.devices) {
        if (!selectedDevices.includes(device.source_id)) continue
        if (bulkProfile) next[device.source_id] = bulkProfile
        else delete next[device.source_id]
      }
      return next
    })
  }

  const assignType = (sourceID: string, typeName: string) => {
    setTypeAssignments(prev => ({ ...prev, [sourceID]: typeName }))
  }

  const applyTypeToAll = () => {
    if (!preview || !bulkType) return
    setTypeAssignments(prev => {
      const next = { ...prev }
      for (const device of preview.devices) {
        if (selectedDevices.includes(device.source_id)) next[device.source_id] = bulkType
      }
      return next
    })
  }

  const apply = async () => {
    if (!preview) return
    setBusy(true); setError('')
    try {
      const snmpProfilesPayload = Object.fromEntries(
        Object.entries(snmpAssignments).filter(([sourceID, profileID]) => profileID && selectedDevices.includes(sourceID)),
      )
      const deviceTypesPayload = Object.fromEntries(
        Object.entries(typeAssignments).filter(([sourceID, typeName]) => typeName && selectedDevices.includes(sourceID)),
      )
      const data = await apiSend<Record<string, unknown>>(`/imports/dude/${importID}/apply`, 'POST', {
        map_ids: selectedMaps,
        device_source_ids: selectedDevices,
        snmp_profiles: snmpProfilesPayload,
        probe_ids: selectedProbes,
        device_types: deviceTypesPayload,
        parent_map_id: parentMap,
        name_prefix: prefix,
        create_links: createLinks,
      })
      setResult(data)
      window.dispatchEvent(new Event('netmonitor:maps-changed'))
      flash('Importación aplicada')
      await load()
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }

  const firstMapID = useMemo(() => {
    if (!result) return ''
    const mapIDs = result.map_ids as Record<string, string> | undefined
    return mapIDs ? Object.values(mapIDs)[0] || '' : ''
  }, [result])

  const assignedSNMP = useMemo(
    () => Object.entries(snmpAssignments).filter(([sourceID, profileID]) => profileID && selectedDevices.includes(sourceID)).length,
    [snmpAssignments, selectedDevices],
  )

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Fase 3 · Migración</span>
          <h1>Importar desde The Dude</h1>
          <p className="page-subtitle">Revisa mapas, dispositivos y enlaces desde SQLite o XML antes de escribir en el inventario.</p>
        </div>
        <div className="page-head-actions">
          <Button variant="secondary" onClick={() => void load()}><RefreshCw size={14} /> Actualizar</Button>
          <Button variant="primary" onClick={() => fileRef.current?.click()} disabled={busy}><Upload size={14} /> {busy ? 'Procesando…' : 'Seleccionar archivo'}</Button>
          <input ref={fileRef} type="file" accept=".db,.sqlite,.sqlite3,.xml,application/xml,text/xml" hidden onChange={upload} />
        </div>
      </div>

      {notice && <div className="notice-ok"><CheckCircle2 size={15} /> {notice}</div>}
      {error && <div className="form-error"><AlertTriangle size={14} /> {error}</div>}

      <div className="import-drop" onClick={() => fileRef.current?.click()}>
        <div className="import-drop-icon"><Database size={26} /></div>
        <div><b>Selecciona el archivo dude.db o XML</b><span>SQLite de The Dude (hasta 256 MB) y exportaciones XML. No se importan credenciales; puedes asignar perfiles SNMP y sondas ya creadas en el preview.</span></div>
      </div>

      <div className="table-card">
        <div className="table-toolbar"><b>Historial de importaciones</b><span className="muted small">El preview queda guardado para auditoría.</span></div>
        {loading ? <div className="page-center"><Spinner /></div> : (
          <div className="data-table imports-table">
            <div className="data-row head"><span>Archivo</span><span>Tipo</span><span>Contenido</span><span>Estado</span><span>Creada</span><span /></div>
            {history.map(item => (
              <div className="data-row" key={item.id}>
                <span className="cell-main"><b>{item.filename}</b><small>{item.created_by}</small></span>
                <span>{item.source_kind === 'sqlite' ? <span className="cred-pill"><Database size={11} /> SQLite</span> : <span className="cred-pill"><FileJson size={11} /> XML</span>}</span>
                <span className="muted small">{item.map_count} mapas · {item.device_count} equipos · {item.link_count} enlaces</span>
                <span><StatusBadge status={item.status === 'applied' ? 'up' : item.status === 'failed' ? 'down' : 'warning'} text={item.status === 'applied' ? 'Aplicada' : item.status === 'failed' ? 'Fallida' : 'Preview'} /></span>
                <span className="muted small">{timeAgo(item.created_at)}</span>
                <span><Button size="sm" variant="ghost" onClick={() => void openHistory(item)}>Ver</Button></span>
              </div>
            ))}
            {!history.length && <EmptyState title="Sin importaciones" message="Sube el archivo de The Dude para generar un preview y elegir qué migrar." icon={<Database size={24} />} />}
          </div>
        )}
      </div>

      <div className="warning-box"><AlertTriangle size={16} /><div><b>El modelo de The Dude es antiguo</b><span>Las credenciales no se migran. En el preview puedes asignar perfiles SNMP y sondas ya creadas; las credenciales RouterOS se asignan después desde el inventario.</span></div></div>

      <Modal open={previewOpen} onClose={() => setPreviewOpen(false)} title="Preview de importación" subtitle={preview ? `${preview.filename} · ${preview.source_kind}` : ''} width={960} footer={<><Button variant="ghost" onClick={() => setPreviewOpen(false)}>Cerrar</Button>{result ? (firstMapID && <Button variant="primary" onClick={() => navigate(`/maps/${firstMapID}`)}><MapIcon size={14} /> Abrir mapa importado</Button>) : <Button variant="primary" onClick={() => void apply()} disabled={busy || !selectedMaps.length || !selectedDevices.length}>{busy ? 'Importando…' : 'Aplicar importación'}</Button>}</>}>
        {preview && <>
          <div className="import-summary">
            <div><b>{preview.maps.length}</b><span>mapas</span></div>
            <div><b>{preview.devices.length}</b><span>equipos</span></div>
            <div><b>{preview.submaps?.length ?? 0}</b><span>submapas</span></div>
            <div><b>{preview.decorations?.length ?? 0}</b><span>decoraciones</span></div>
            <div><b>{preview.links.length}</b><span>enlaces</span></div>
            <div><b>{preview.warnings.length}</b><span>avisos</span></div>
          </div>
          {preview.warnings.length > 0 && <div className="warning-box"><AlertTriangle size={15} /><div><b>Avisos</b>{preview.warnings.map((w, i) => <span key={i}>{w}</span>)}</div></div>}
          {!result && <div className="form-grid two">
            <Field label="Mapa padre (opcional)"><select className="input" value={parentMap} onChange={e => setParentMap(e.target.value)}><option value="">— Raíz</option>{maps.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}</select></Field>
            <Field label="Prefijo de nombres" hint="Se aplica a mapas y equipos importados."><input className="input" value={prefix} onChange={e => setPrefix(e.target.value)} placeholder="Dude · " /></Field>
          </div>}
          {!result && <div className="check-row"><label><input type="checkbox" checked={createLinks} onChange={e => setCreateLinks(e.target.checked)} /> Crear enlaces entre dispositivos importados</label></div>}
          <div className="import-columns">
            <div>
              <h4 className="section-mini-title">Mapas <span>{selectedMaps.length}/{preview.maps.length}</span></h4>
              <div className="target-picker compact">
                {preview.maps.map(m => <label key={m.source_id} className={selectedMaps.includes(m.source_id) ? 'selected' : ''}><input type="checkbox" checked={selectedMaps.includes(m.source_id)} onChange={e => setSelectedMaps(e.target.checked ? [...selectedMaps, m.source_id] : selectedMaps.filter(x => x !== m.source_id))} /><span><b>{m.name}</b><small>{m.device_count} equipos</small></span></label>)}
              </div>
            </div>
            <div>
              <h4 className="section-mini-title">Dispositivos <span>{selectedDevices.length}/{preview.devices.length}</span></h4>
              <div className="target-picker compact scroll">
                {preview.devices.map(d => <label key={d.source_id} className={selectedDevices.includes(d.source_id) ? 'selected' : ''}><input type="checkbox" checked={selectedDevices.includes(d.source_id)} onChange={e => setSelectedDevices(e.target.checked ? [...selectedDevices, d.source_id] : selectedDevices.filter(x => x !== d.source_id))} /><span><b>{d.name}</b><small className="mono">{d.address || 'sin IP'} · {d.device_type}</small></span></label>)}
              </div>
            </div>
          </div>
          {!result && (
            <div className="snmp-assign">
              <div className="snmp-assign-head">
                <div>
                  <h4 className="section-mini-title"><KeyRound size={14} /> Perfil SNMP <span>{assignedSNMP} asignados</span></h4>
                  <span className="muted small">Opcional. Se aplica a los equipos seleccionados; puedes usar el mismo en todos.</span>
                </div>
                {snmpProfiles.length > 0 && (
                  <div className="snmp-bulk">
                    <select className="input" value={bulkProfile} onChange={e => setBulkProfile(e.target.value)}>
                      <option value="">Sin perfil</option>
                      {snmpProfiles.map(p => <option key={p.id} value={p.id}>{p.name} · {p.version}</option>)}
                    </select>
                    <Button size="sm" variant="secondary" onClick={applySNMPToAll} disabled={!selectedDevices.length}>Aplicar a todos</Button>
                  </div>
                )}
              </div>
              {!snmpProfiles.length ? (
                <span className="muted small">No hay perfiles SNMP creados. Créalos en Ajustes y servicios → Perfiles SNMP y vuelve a abrir el preview.</span>
              ) : selectedDevices.length === 0 ? (
                <span className="muted small">Selecciona al menos un equipo para asignarle perfil.</span>
              ) : (
                <div className="snmp-assign-list">
                  {preview.devices.filter(d => selectedDevices.includes(d.source_id)).map(d => (
                    <div key={d.source_id} className="snmp-assign-row">
                      <span><b>{d.name}</b><small className="mono">{d.address || 'sin IP'}</small></span>
                      <select className="input" value={snmpAssignments[d.source_id] || ''} onChange={e => assignSNMP(d.source_id, e.target.value)}>
                        <option value="">Sin perfil</option>
                        {snmpProfiles.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
                      </select>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
          {!result && (
            <div className="snmp-assign">
              <div className="snmp-assign-head">
                <div>
                  <h4 className="section-mini-title"><Server size={14} /> Tipo de dispositivo</h4>
                  <span className="muted small">Se aplica a los equipos seleccionados. Por defecto se mantiene el tipo detectado en The Dude.</span>
                </div>
                <div className="snmp-bulk">
                  <select className="input" value={bulkType} onChange={e => setBulkType(e.target.value)}>
                    <option value="">Mantener tipo detectado</option>
                    {deviceTypes.map(t => <option key={t.name} value={t.name}>{t.label}</option>)}
                  </select>
                  <Button size="sm" variant="secondary" onClick={applyTypeToAll} disabled={!bulkType || !selectedDevices.length}>Aplicar a todos</Button>
                </div>
              </div>
              {!deviceTypes.length ? (
                <span className="muted small">No hay tipos de dispositivo disponibles.</span>
              ) : selectedDevices.length === 0 ? (
                <span className="muted small">Selecciona al menos un equipo.</span>
              ) : (
                <div className="snmp-assign-list">
                  {preview.devices.filter(d => selectedDevices.includes(d.source_id)).map(d => (
                    <div key={d.source_id} className="snmp-assign-row">
                      <span><b>{d.name}</b><small className="mono">{d.address || 'sin IP'}</small></span>
                      <select className="input" value={typeAssignments[d.source_id] || d.device_type || 'generic'} onChange={e => assignType(d.source_id, e.target.value)}>
                        {deviceTypes.map(t => <option key={t.name} value={t.name}>{t.label}</option>)}
                      </select>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
          {!result && (
            <div className="snmp-assign">
              <div className="snmp-assign-head">
                <div>
                  <h4 className="section-mini-title"><Activity size={14} /> Sondas para los equipos <span>{selectedProbes.length} seleccionadas</span></h4>
                  <span className="muted small">Se añaden a todos los equipos seleccionados; las que ya existan no se duplican. Para sondas SNMP, asigna también un perfil SNMP.</span>
                </div>
                <div className="snmp-bulk">
                  <Button size="sm" variant="secondary" onClick={() => setSelectedProbes(probes.map(p => p.id))} disabled={!probes.length}>Seleccionar todas</Button>
                  <Button size="sm" variant="ghost" onClick={() => setSelectedProbes([])} disabled={!selectedProbes.length}>Limpiar</Button>
                </div>
              </div>
              <div className="probe-checks">
                {probes.map(p => (
                  <label key={p.id} className={`probe-check ${selectedProbes.includes(p.id) ? 'active' : ''}`}>
                    <input type="checkbox" checked={selectedProbes.includes(p.id)} onChange={e => setSelectedProbes(e.target.checked ? [...selectedProbes, p.id] : selectedProbes.filter(id => id !== p.id))} />
                    <span><b>{p.name}</b><small>{p.probe_type} · cada {p.default_interval_sec}s</small></span>
                  </label>
                ))}
                {!probes.length && <span className="muted small">No hay sondas disponibles.</span>}
              </div>
            </div>
          )}
          {result && <div className="notice-ok"><CheckCircle2 size={15} /><div><b>Importación completada</b><span>{String(result.created_maps ?? 0)} mapas, {String(result.created_devices ?? 0)} equipos nuevos, {String(result.reused_devices ?? 0)} reutilizados, {String(result.device_types_assigned ?? 0)} con tipo asignado, {String(result.snmp_assigned ?? 0)} con perfil SNMP, {String(result.probes_added ?? 0)} sondas añadidas, {String(result.created_submaps ?? 0)} submapas, {String(result.created_decorations ?? 0)} decoraciones, {String(result.created_links ?? 0)} enlaces{Number(result.skipped_links ?? 0) > 0 ? ` (${String(result.skipped_links)} omitidos)` : ''}.</span>{Array.isArray(result.warnings) && (result.warnings as string[]).map((w, i) => <span key={i}>{w}</span>)}</div></div>}
        </>}
      </Modal>
    </div>
  )
}
