import { useEffect, useMemo, useState } from 'react'
import {
  Activity,
  BookOpen,
  Braces,
  Check,
  ChevronDown,
  ChevronRight,
  Code2,
  Cpu,
  FlaskConical,
  Globe,
  Mail,
  Network,
  Pencil,
  Play,
  Plus,
  Radar,
  Radio,
  Search,
  Server,
  Terminal,
  Timer,
  Trash2,
  Wifi,
} from 'lucide-react'
import { apiGet, apiSend } from '../api'
import { confirmAction } from '../confirm'
import type { Device, FunctionDef, MapInfo, Probe } from '../types'
import { Button, EmptyState, Field, Modal, Spinner } from '../ui'

const PROBE_ICON: Record<string, React.ReactNode> = {
  icmp: <Wifi size={17} />,
  snmp: <Network size={17} />,
  tcp: <Network size={17} />,
  http: <Globe size={17} />,
  dns: <Radar size={17} />,
  function: <Code2 size={17} />,
  logic: <Cpu size={17} />,
  udp: <Radio size={17} />,
  ssh: <Terminal size={17} />,
  smtp: <Mail size={17} />,
  ftp: <Server size={17} />,
}

type MibObject = { module: string; oid: string; name: string; syntax: string; access: string; description?: string }

function mibFunctionName(name: string) {
  let out = (name || '').toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '').replace(/_+/g, '_')
  if (!out || /^[0-9]/.test(out)) out = `mib_${out}`
  return out
}

function buildMibTree(objects: MibObject[]) {
  type Node = { segment: string; oid: string; object?: MibObject; children: Map<string, Node> }
  const root: Node = { segment: '', oid: '', children: new Map() }
  for (const obj of objects) {
    let node = root
    let oid = ''
    for (const segment of obj.oid.split('.')) {
      oid = oid ? `${oid}.${segment}` : segment
      if (!node.children.has(segment)) node.children.set(segment, { segment, oid, children: new Map() })
      node = node.children.get(segment)!
    }
    node.object = obj
  }
  return root
}

function MibTreeNode({ node, depth, selected, onSelect }: { node: any; depth: number; selected: MibObject | null; onSelect: (o: MibObject) => void }) {
  const [open, setOpen] = useState(depth < 3)
  const children = Array.from(node.children.values()) as any[]
  const isSelected = selected?.oid === node.oid && !!node.object
  return (
    <div>
      <div className={`mib-tree-row ${isSelected ? 'selected' : ''}`} style={{ paddingLeft: 8 + depth * 14 }} onClick={() => node.object && onSelect(node.object)}>
        <button className="mib-tree-toggle" onClick={e => { e.stopPropagation(); setOpen(!open) }} disabled={!children.length}>
          {children.length ? (open ? <ChevronDown size={12} /> : <ChevronRight size={12} />) : <span />}
        </button>
        <span className="mono mib-tree-oid">{node.segment}</span>
        <span className="mib-tree-name">{node.object?.name || (children.length ? `${children.length} nodos` : '')}</span>
        {node.object?.access && <small>{node.object.access}</small>}
      </div>
      {open && children.map((child: any) => (
        <MibTreeNode key={child.oid} node={child} depth={depth + 1} selected={selected} onSelect={onSelect} />
      ))}
    </div>
  )
}

export default function ProbesPage() {
  const [probes, setProbes] = useState<Probe[]>([])
  const [functions, setFunctions] = useState<FunctionDef[]>([])
  const [loading, setLoading] = useState(true)
  const [tab, setTab] = useState<'probes' | 'functions' | 'mibs'>('probes')
  const [devices, setDevices] = useState<Device[]>([])
  const [dslTarget, setDslTarget] = useState<'available' | 'value' | 'warning' | 'error'>('value')
  const [dslDevice, setDslDevice] = useState('')
  const [dslTest, setDslTest] = useState('')
  const [mibObjects, setMibObjects] = useState<MibObject[]>([])
  const [mibModules, setMibModules] = useState<any[]>([])
  const [mibQ, setMibQ] = useState('')
  const [mibModule, setMibModule] = useState('')
  const [mibSelected, setMibSelected] = useState<MibObject | null>(null)
  const [mibLoading, setMibLoading] = useState(false)
  const [labelOpen, setLabelOpen] = useState(false)
  const [labelMaps, setLabelMaps] = useState<MapInfo[]>([])
  const [labelMapID, setLabelMapID] = useState('')
  const [labelElements, setLabelElements] = useState<any[]>([])
  const [labelElementID, setLabelElementID] = useState('')
  const [probeOpen, setProbeOpen] = useState(false)
  const [probeEditing, setProbeEditing] = useState<Probe | null>(null)
  const [fnOpen, setFnOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [form, setForm] = useState({
    name: '', type: 'icmp', description: '', config: '{}', available: '', warning: '', errorExpr: '',
    valueExpr: '', unit: 'ms', interval: 30, timeout: 2000, retries: 3,
  })
  const [fnForm, setFnForm] = useState({ name: '', description: '', expression: '', unit: '%' })

  const load = async () => {
    setLoading(true)
    try {
      const [p, f, m] = await Promise.all([
        apiGet<Probe[]>('/probes'),
        apiGet<FunctionDef[]>('/functions'),
        apiGet<any[]>('/mibs').catch(() => []),
      ])
      setProbes(p)
      setFunctions(f)
      setMibModules(m)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { void load() }, [])

  const loadMibObjects = async (q = mibQ, module = mibModule) => {
    setMibLoading(true)
    try {
      const params = new URLSearchParams({ q, limit: '1500' })
      if (module) params.set('module', module)
      setMibObjects(await apiGet<MibObject[]>(`/mibs/objects?${params.toString()}`))
    } catch {
      setMibObjects([])
    } finally {
      setMibLoading(false)
    }
  }
  useEffect(() => {
    if (tab !== 'mibs' || mibObjects.length) return
    void loadMibObjects('', '')
  }, [tab])

  useEffect(() => {
    if (!probeOpen) return
    apiGet<Device[]>('/devices').then(setDevices).catch(() => {})
    if (dslDevice === '' && devices[0]) setDslDevice(devices[0].id)
  }, [probeOpen])

  const openProbeModal = (probe?: Probe) => {
    setError('')
    setProbeEditing(probe || null)
    setForm(probe
      ? {
          name: probe.name, type: probe.probe_type, description: probe.description, config: JSON.stringify(probe.config || {}, null, 2),
          available: probe.available_expression || '', warning: probe.warning_expression, errorExpr: probe.error_expression, valueExpr: probe.value_expression,
          unit: probe.unit, interval: probe.default_interval_sec, timeout: probe.default_timeout_ms, retries: probe.default_down_retries,
        }
      : {
          name: '', type: 'icmp', description: '', config: '{}', available: '', warning: '', errorExpr: '',
          valueExpr: '', unit: 'ms', interval: 30, timeout: 2000, retries: 3,
        })
    setProbeOpen(true)
  }

  const saveProbe = async () => {
    setError('')
    setSaving(true)
    try {
      let config: unknown
      try { config = JSON.parse(form.config) } catch { throw new Error('El JSON de configuración no es válido') }
      const payload = {
        name: form.name, probe_type: form.type, description: form.description, config,
        available_expression: form.available,
        warning_expression: form.warning, error_expression: form.errorExpr, value_expression: form.valueExpr,
        unit: form.unit, default_interval_sec: form.interval, default_timeout_ms: form.timeout,
        default_down_retries: form.retries,
      }
      if (probeEditing) await apiSend(`/probes/${probeEditing.id}`, 'PUT', payload)
      else await apiSend('/probes', 'POST', payload)
      setProbeOpen(false)
      setProbeEditing(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const deleteProbe = async (probe: Probe) => {
    if (!await confirmAction(`¿Eliminar la sonda "${probe.name}"?`)) return
    try {
      await apiSend(`/probes/${probe.id}`, 'DELETE')
      await load()
    } catch (e) {
      alert((e as Error).message)
    }
  }

  const createFunction = async () => {
    setError('')
    setSaving(true)
    try {
      await apiSend('/functions', 'POST', {
        name: fnForm.name, description: fnForm.description, expression_code: fnForm.expression, unit: fnForm.unit,
      })
      setFnOpen(false)
      setFnForm({ name: '', description: '', expression: '', unit: '%' })
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const insertDsl = (snippet: string) => {
    const key = dslTarget === 'available' ? 'available' : dslTarget === 'warning' ? 'warning' : dslTarget === 'error' ? 'errorExpr' : 'valueExpr'
    setForm(prev => ({ ...prev, [key]: (prev as any)[key] ? `${(prev as any)[key]} ${snippet}` : snippet }))
  }

  const testDsl = async () => {
    setError(''); setDslTest('')
    try {
      const data = await apiSend<{ result: string; type: string }>('/tools/evaluate-dsl', 'POST', {
        device_id: dslDevice, expression: (form as any)[dslTarget === 'available' ? 'available' : dslTarget === 'warning' ? 'warning' : dslTarget === 'error' ? 'errorExpr' : 'valueExpr'],
      })
      setDslTest(`${data.result} (${data.type})`)
    } catch (e) { setError((e as Error).message) }
  }

  const createFunctionFromMib = (obj: MibObject) => {
    setMibSelected(obj)
    setFnForm({
      name: mibFunctionName(obj.name), description: `${obj.module} · ${obj.name}`,
      expression: `oid("${obj.oid.endsWith('.0') ? obj.oid : obj.oid + '.0'}")`, unit: '',
    })
    setError('')
    setFnOpen(true)
  }

  const createProbeFromMib = (obj: MibObject) => {
    setMibSelected(obj)
    const oid = obj.oid.endsWith('.0') ? obj.oid : obj.oid + '.0'
    setForm({
      name: `SNMP · ${obj.name}`, type: 'snmp', description: `${obj.module} · ${obj.oid}`,
      config: JSON.stringify({ oids: [oid] }, null, 2), available: '', warning: '', errorExpr: '',
      valueExpr: `oid("${oid}")`, unit: '', interval: 60, timeout: 2500, retries: 2,
    })
    setError('')
    setProbeEditing(null)
    setProbeOpen(true)
  }

  const openMibLabel = async (obj: MibObject) => {
    setMibSelected(obj)
    setError('')
    try {
      const maps = await apiGet<MapInfo[]>('/maps')
      setLabelMaps(maps)
      setLabelMapID(maps[0]?.id || '')
      setLabelElementID('')
      setLabelElements([])
      setLabelOpen(true)
      if (maps[0]) await loadLabelElements(maps[0].id)
    } catch (e) { setError((e as Error).message) }
  }

  const loadLabelElements = async (mapId: string) => {
    const canvas = await apiGet<any>(`/maps/${mapId}/canvas`)
    setLabelElements((canvas.elements || []).filter((e: any) => e.element_type === 'device' && e.device_id))
  }

  const applyMibLabel = async () => {
    if (!mibSelected || !labelMapID || !labelElementID) return
    setSaving(true); setError('')
    try {
      const data = await apiSend<{ function: string }>('/mibs/label', 'POST', {
        oid: mibSelected.oid, name: mibSelected.name, map_id: labelMapID, element_id: labelElementID,
      })
      setLabelOpen(false)
      await load()
      alert(`Función [${data.function}()] añadida a la etiqueta del elemento.`)
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const mibTree = useMemo(() => buildMibTree(mibObjects), [mibObjects])

  if (loading) return <div className="page-center"><Spinner /></div>

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Motor de monitorización</span>
          <h1>Sondas y funciones</h1>
          <p className="page-subtitle">Plantillas de chequeo reutilizables y expresiones SNMP estilo The Dude.</p>
        </div>
        <div className="page-head-actions">
          <Button variant="secondary" onClick={() => { setError(''); setFnOpen(true) }}><Braces size={15} /> Nueva función</Button>
          <Button variant="primary" onClick={() => openProbeModal()}><Plus size={15} /> Nueva sonda</Button>
        </div>
      </div>

      <div className="segmented page-segmented">
        <button className={tab === 'probes' ? 'active' : ''} onClick={() => setTab('probes')}><Activity size={15} /> Sondas <span>{probes.length}</span></button>
        <button className={tab === 'functions' ? 'active' : ''} onClick={() => setTab('functions')}><Code2 size={15} /> Funciones <span>{functions.length}</span></button>
        <button className={tab === 'mibs' ? 'active' : ''} onClick={() => setTab('mibs')}><BookOpen size={15} /> MIB browser <span>{mibObjects.length}</span></button>
      </div>

      {tab === 'probes' ? (
        <div className="probe-grid">
          {probes.map(p => (
            <div className="probe-card" key={p.id}>
              <div className="probe-card-head">
                <div className={`probe-icon probe-${p.probe_type}`}>{PROBE_ICON[p.probe_type] || <Activity size={17} />}</div>
                <div><b>{p.name}</b><span>{p.probe_type.toUpperCase()} · cada {p.default_interval_sec}s</span></div>
                {p.is_builtin && <span className="builtin-pill">Built-in</span>}
              </div>
              <p>{p.description || 'Sin descripción.'}</p>
              <div className="probe-meta">
                <span><Timer size={12} /> {p.default_timeout_ms} ms</span>
                <span>{p.default_down_retries} reintentos</span>
                {p.unit && <span className="unit-chip">{p.unit}</span>}
              </div>
              {Object.keys(p.config || {}).length > 0 && (
                <div className="probe-config mono">{JSON.stringify(p.config, null, 0).slice(0, 120)}</div>
              )}
              {(p.available_expression || p.warning_expression || p.error_expression) && (
                <div className="probe-expr">
                  {p.available_expression && <div><span>AVAIL</span><code>{p.available_expression}</code></div>}
                  {p.warning_expression && <div><span>WARN</span><code>{p.warning_expression}</code></div>}
                  {p.error_expression && <div><span>ERR</span><code>{p.error_expression}</code></div>}
                </div>
              )}
              <div className="probe-card-actions">
                <Button variant="secondary" size="sm" onClick={() => openProbeModal(p)}><Pencil size={13} /> Editar</Button>
                {!p.is_builtin && <Button variant="danger" size="sm" onClick={() => deleteProbe(p)}><Trash2 size={13} /></Button>}
              </div>
            </div>
          ))}
          {!probes.length && <EmptyState title="Sin sondas" message="Crea la primera sonda de monitorización." icon={<Activity size={24} />} />}
        </div>
      ) : tab === 'functions' ? (
        <div className="function-grid">
          {functions.map(f => (
            <div className="function-card" key={f.id}>
              <div className="function-head"><span className="fn-name">ƒ {f.name}()</span>{f.unit && <span className="unit-chip">{f.unit}</span>}{f.is_builtin && <span className="builtin-pill">Built-in</span>}</div>
              <p>{f.description || 'Función personalizada.'}</p>
              <pre>{f.expression_code}</pre>
            </div>
          ))}
          {!functions.length && <EmptyState title="Sin funciones" message="Añade funciones DSL para reutilizar en etiquetas y sondas." icon={<Code2 size={24} />} />}
        </div>
      ) : (
        <div className="mib-browser">
          <div className="mib-toolbar">
            <div className="search-inline">
              <Search size={15} />
              <input className="input" value={mibQ} onChange={e => setMibQ(e.target.value)} placeholder="Buscar por nombre u OID…" onKeyDown={e => { if (e.key === 'Enter') void loadMibObjects() }} />
            </div>
            <select className="input" value={mibModule} onChange={e => { setMibModule(e.target.value); void loadMibObjects(mibQ, e.target.value) }}>
              <option value="">Todos los módulos</option>
              {mibModules.map(m => <option key={m.module} value={m.module}>{m.module} ({m.objects})</option>)}
            </select>
            <Button variant="secondary" onClick={() => loadMibObjects()} disabled={mibLoading}><Search size={14} /> {mibLoading ? 'Buscando…' : 'Buscar'}</Button>
          </div>
          <div className="mib-layout">
            <div className="mib-tree">
              {mibLoading ? <Spinner label="Cargando objetos" /> : (
                Array.from((mibTree.children as Map<string, any>).values()).map((child: any) => (
                  <MibTreeNode key={child.oid} node={child} depth={0} selected={mibSelected} onSelect={setMibSelected} />
                ))
              )}
              {!mibObjects.length && !mibLoading && <EmptyState title="Sin objetos MIB" message="Importa una MIB en Ajustes o busca por nombre." icon={<BookOpen size={22} />} />}
            </div>
            <div className="mib-detail">
              {mibSelected ? (
                <>
                  <span className="eyebrow">{mibSelected.module}</span>
                  <h3>{mibSelected.name}</h3>
                  <div className="mono mib-oid">{mibSelected.oid}</div>
                  <div className="mib-chips"><span>{mibSelected.syntax}</span><span>{mibSelected.access}</span></div>
                  <p>{mibSelected.description || 'Sin descripción en la MIB.'}</p>
                  <div className="mib-actions">
                    <Button variant="secondary" size="sm" onClick={() => createFunctionFromMib(mibSelected)}><Braces size={13} /> Crear función</Button>
                    <Button variant="secondary" size="sm" onClick={() => createProbeFromMib(mibSelected)}><Activity size={13} /> Crear sonda</Button>
                    <Button variant="primary" size="sm" onClick={() => openMibLabel(mibSelected)}><Plus size={13} /> Añadir a etiqueta</Button>
                  </div>
                </>
              ) : (
                <EmptyState title="Selecciona un objeto" message="Explora el árbol o busca un OID para crear funciones, sondas o etiquetas." icon={<BookOpen size={22} />} />
              )}
            </div>
          </div>
        </div>
      )}

      <Modal open={probeOpen} onClose={() => { setProbeOpen(false); setProbeEditing(null) }} title={probeEditing ? 'Editar sonda' : 'Nueva sonda'} subtitle="Plantilla reutilizable. Después se asigna a uno o varios equipos." width={680} footer={<><Button variant="ghost" onClick={() => { setProbeOpen(false); setProbeEditing(null) }}>Cancelar</Button><Button variant="primary" onClick={saveProbe} disabled={saving}>{saving ? 'Guardando…' : probeEditing ? 'Guardar cambios' : 'Crear sonda'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} placeholder="SNMP Temperatura" autoFocus /></Field>
          <Field label="Tipo"><select className="input" value={form.type} onChange={e => setForm({ ...form, type: e.target.value })}><option value="icmp">ICMP</option><option value="snmp">SNMP</option><option value="tcp">TCP</option><option value="http">HTTP</option><option value="dns">DNS</option><option value="udp">UDP</option><option value="ssh">SSH</option><option value="smtp">SMTP</option><option value="ftp">FTP</option><option value="function">Función</option><option value="logic">Lógica AND/OR</option></select></Field>
          <Field label="Descripción"><input className="input" value={form.description} onChange={e => setForm({ ...form, description: e.target.value })} /></Field>
          <Field label="Unidad"><input className="input" value={form.unit} onChange={e => setForm({ ...form, unit: e.target.value })} placeholder="ms, %, °C" /></Field>
          <Field label="Intervalo (s)"><input className="input" type="number" value={form.interval} onChange={e => setForm({ ...form, interval: Number(e.target.value) })} /></Field>
          <Field label="Timeout (ms)"><input className="input" type="number" value={form.timeout} onChange={e => setForm({ ...form, timeout: Number(e.target.value) })} /></Field>
          <Field label="Reintentos"><input className="input" type="number" value={form.retries} onChange={e => setForm({ ...form, retries: Number(e.target.value) })} /></Field>
        </div>
        <Field label="Configuración JSON" hint={form.type === 'logic'
          ? 'Lógica: {"mode":"all","service_ids":[]}. "all" exige que todos los hijos estén OK; "any" basta con uno. Sin service_ids evalúa el equipo; con scope "map"/"submap" agrega todos los servicios del mapa o de sus submapas.'
          : 'Ej. {"oids":["1.3.6.1.2.1.1.3.0"]} o {"port":443,"scheme":"https"}'}>
          <textarea className="input textarea mono" rows={4} value={form.config} onChange={e => setForm({ ...form, config: e.target.value })} />
        </Field>
        <div className="expr-grid">
          <Field label="Disponibilidad (available)" hint="Si devuelve false o vacío, la sonda pasa a caída."><input className="input mono" value={form.available} onChange={e => setForm({ ...form, available: e.target.value })} placeholder='oid("1.3.6.1.2.1.1.3.0") > 0' /></Field>
          <Field label="Valor a graficar"><input className="input mono" value={form.valueExpr} onChange={e => setForm({ ...form, valueExpr: e.target.value })} placeholder='oid("1.3.6.1.4.1...")' /></Field>
          <Field label="Expresión de advertencia"><input className="input mono" value={form.warning} onChange={e => setForm({ ...form, warning: e.target.value })} placeholder='if(oid("...") > 80, "CPU alta", "")' /></Field>
          <Field label="Expresión de error"><input className="input mono" value={form.errorExpr} onChange={e => setForm({ ...form, errorExpr: e.target.value })} placeholder='if(oid("...") > 95, "CPU crítica", "")' /></Field>
        </div>

        <div className="dsl-builder">
          <div className="dsl-builder-head">
            <b><FlaskConical size={14} /> Constructor DSL</b>
            <select className="input" value={dslTarget} onChange={e => setDslTarget(e.target.value as any)}>
              <option value="available">Disponibilidad</option>
              <option value="value">Valor</option>
              <option value="warning">Advertencia</option>
              <option value="error">Error</option>
            </select>
            <select className="input" value={dslDevice} onChange={e => setDslDevice(e.target.value)}>
              <option value="">Equipo para probar…</option>
              {devices.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}
            </select>
            <Button variant="secondary" size="sm" onClick={testDsl} disabled={!dslDevice}><Play size={13} /> Probar</Button>
          </div>
          <div className="dsl-palette">
            {['oid("1.3.6.1.2.1.1.3.0")', 'rate("1.3.6.1.2.1.2.2.1.10.1")', 'if(oid("...") > 0, "OK", "FALLO")', 'round(oid("...") / 10, 1)', 'avg(oid_column("..."))', 'uptime_format(oid("1.3.6.1.2.1.1.3.0"))'].map(snippet => (
              <button key={snippet} className="dsl-chip mono" onClick={() => insertDsl(snippet)}>{snippet.split('(')[0]}()</button>
            ))}
          </div>
          {dslTest && <div className="dsl-result mono"><Check size={13} /> {dslTest}</div>}
        </div>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={fnOpen} onClose={() => setFnOpen(false)} title="Nueva función DSL" subtitle="Se podrá usar como [nombre()] en etiquetas y sondas." footer={<><Button variant="ghost" onClick={() => setFnOpen(false)}>Cancelar</Button><Button variant="primary" onClick={createFunction} disabled={saving}>{saving ? 'Creando…' : 'Crear función'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre" hint="Sin paréntesis ni espacios."><input className="input mono" value={fnForm.name} onChange={e => setFnForm({ ...fnForm, name: e.target.value })} placeholder="temp_chasis" autoFocus /></Field>
          <Field label="Unidad"><input className="input" value={fnForm.unit} onChange={e => setFnForm({ ...fnForm, unit: e.target.value })} /></Field>
        </div>
        <Field label="Descripción"><input className="input" value={fnForm.description} onChange={e => setFnForm({ ...fnForm, description: e.target.value })} /></Field>
        <Field label="Expresión" hint="Funciones disponibles: oid(), oid_column(), rate(), if(), concatenate(), round(), avg(), bitrate(), uptime_format().">
          <textarea className="input textarea mono" rows={4} value={fnForm.expression} onChange={e => setFnForm({ ...fnForm, expression: e.target.value })} placeholder='round(oid("1.3.6.1.4.1.14988.1.1.3.10.0") / 10, 1)' />
        </Field>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={labelOpen} onClose={() => setLabelOpen(false)} title="Añadir OID a etiqueta" subtitle={mibSelected ? `${mibSelected.name} · ${mibSelected.oid}` : ''} width={620}
        footer={<><Button variant="ghost" onClick={() => setLabelOpen(false)}>Cancelar</Button><Button variant="primary" onClick={applyMibLabel} disabled={saving || !labelElementID}>{saving ? 'Aplicando…' : 'Añadir a etiqueta'}</Button></>}>
        <div className="form-grid two">
          <Field label="Mapa">
            <select className="input" value={labelMapID} onChange={async e => { setLabelMapID(e.target.value); setLabelElementID(''); await loadLabelElements(e.target.value) }}>
              <option value="">— Selecciona mapa</option>
              {labelMaps.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
            </select>
          </Field>
          <Field label="Dispositivo del mapa">
            <select className="input" value={labelElementID} onChange={e => setLabelElementID(e.target.value)}>
              <option value="">— Selecciona nodo</option>
              {labelElements.map(e => <option key={e.id} value={e.id}>{e.device?.name || e.rendered_label || e.id}</option>)}
            </select>
          </Field>
        </div>
        <p className="muted small">Se creará una función DSL con el OID y se añadirá a la plantilla de etiqueta del nodo. Si la función ya existe, se reutiliza.</p>
        {error && <div className="form-error">{error}</div>}
      </Modal>
    </div>
  )
}
