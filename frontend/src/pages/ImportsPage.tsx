import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { AlertTriangle, CheckCircle2, Database, FileJson, Map as MapIcon, RefreshCw, Upload } from 'lucide-react'
import { apiGet, apiSend, apiUpload } from '../api'
import type { DudeImport, DudePreview, MapInfo } from '../types'
import { Button, EmptyState, Field, Modal, Spinner, StatusBadge, timeAgo } from '../ui'

export default function ImportsPage() {
  const [history, setHistory] = useState<DudeImport[]>([])
  const [maps, setMaps] = useState<MapInfo[]>([])
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
      const [h, m] = await Promise.all([apiGet<DudeImport[]>('/imports/dude'), apiGet<MapInfo[]>('/maps')])
      setHistory(h); setMaps(m)
    } catch (e) { setError((e as Error).message) } finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

  const flash = (msg: string) => { setNotice(msg); window.setTimeout(() => setNotice(''), 5000) }

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
      setParentMap('')
      setPrefix('')
      setCreateLinks(true)
      setPreviewOpen(true)
      setResult((full.result as Record<string, unknown>) || null)
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }

  const apply = async () => {
    if (!preview) return
    setBusy(true); setError('')
    try {
      const data = await apiSend<Record<string, unknown>>(`/imports/dude/${importID}/apply`, 'POST', {
        map_ids: selectedMaps,
        device_source_ids: selectedDevices,
        parent_map_id: parentMap,
        name_prefix: prefix,
        create_links: createLinks,
      })
      setResult(data)
      flash('Importación aplicada')
      await load()
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }

  const firstMapID = useMemo(() => {
    if (!result) return ''
    const mapIDs = result.map_ids as Record<string, string> | undefined
    return mapIDs ? Object.values(mapIDs)[0] || '' : ''
  }, [result])

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
        <div><b>Selecciona el archivo dude.db o XML</b><span>SQLite de The Dude (hasta 64 MB) y exportaciones XML. No se importan contraseñas ni comunidades SNMP.</span></div>
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

      <div className="warning-box"><AlertTriangle size={16} /><div><b>El modelo de The Dude es antiguo</b><span>Las credenciales, sondas y perfiles SNMP no se migran. Revisa los equipos importados y asigna perfiles SNMP/RouterOS cifrados antes de monitorizarlos.</span></div></div>

      <Modal open={previewOpen} onClose={() => setPreviewOpen(false)} title="Preview de importación" subtitle={preview ? `${preview.filename} · ${preview.source_kind}` : ''} width={960} footer={<><Button variant="ghost" onClick={() => setPreviewOpen(false)}>Cerrar</Button>{result ? (firstMapID && <Button variant="primary" onClick={() => navigate(`/maps/${firstMapID}`)}><MapIcon size={14} /> Abrir mapa importado</Button>) : <Button variant="primary" onClick={() => void apply()} disabled={busy || !selectedMaps.length || !selectedDevices.length}>{busy ? 'Importando…' : 'Aplicar importación'}</Button>}</>}>
        {preview && <>
          <div className="import-summary">
            <div><b>{preview.maps.length}</b><span>mapas</span></div>
            <div><b>{preview.devices.length}</b><span>equipos</span></div>
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
          {result && <div className="notice-ok"><CheckCircle2 size={15} /><div><b>Importación completada</b><span>{String(result.created_maps ?? 0)} mapas, {String(result.created_devices ?? 0)} equipos nuevos, {String(result.reused_devices ?? 0)} reutilizados, {String(result.created_links ?? 0)} enlaces.</span>{Array.isArray(result.warnings) && (result.warnings as string[]).map((w, i) => <span key={i}>{w}</span>)}</div></div>}
        </>}
      </Modal>
    </div>
  )
}
