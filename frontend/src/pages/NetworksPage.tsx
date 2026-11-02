import { useEffect, useMemo, useState } from 'react'
import {
  Check,
  Clock3,
  Download,
  ListChecks,
  Network,
  Pencil,
  Play,
  Plus,
  Radar,
  RefreshCcw,
  Server,
  ShieldBan,
  Trash2,
  Upload,
} from 'lucide-react'
import { apiGet, apiSend } from '../api'
import { confirmAction } from '../confirm'
import type {
  AddressList,
  AddressListEntry,
  Agent,
  DiscoveryHost,
  DiscoveryNetwork,
  DiscoveryRun,
  MapInfo,
  Probe,
  SnmpProfile,
} from '../types'
import { Button, EmptyState, Field, Modal, Spinner, StatusDot, formatDuration, timeAgo } from '../ui'

function futureLabel(iso?: string) {
  if (!iso) return '—'
  const diff = (new Date(iso).getTime() - Date.now()) / 1000
  if (diff <= 0) return 'pendiente'
  return `en ${formatDuration(diff)}`
}

const EMPTY_NETWORK = {
  name: '', cidr: '', address_list_id: '', map_id: '', agent_id: '', snmp_profile_id: '',
  probe_ids: [] as string[], interval_sec: 3600, mode: 'fast', name_preference: 'dns_snmp_ip',
  auto_create: false, is_enabled: true,
}

export default function NetworksPage() {
  const [tab, setTab] = useState<'networks' | 'lists'>('networks')
  const [networks, setNetworks] = useState<DiscoveryNetwork[]>([])
  const [lists, setLists] = useState<AddressList[]>([])
  const [maps, setMaps] = useState<MapInfo[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [profiles, setProfiles] = useState<SnmpProfile[]>([])
  const [probes, setProbes] = useState<Probe[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const [networkOpen, setNetworkOpen] = useState(false)
  const [networkEditing, setNetworkEditing] = useState<DiscoveryNetwork | null>(null)
  const [networkForm, setNetworkForm] = useState({ ...EMPTY_NETWORK })

  const [listOpen, setListOpen] = useState(false)
  const [listEditing, setListEditing] = useState<AddressList | null>(null)
  const [listForm, setListForm] = useState({ name: '', description: '', color: '#0f766e', kind: 'allow' })

  const [entriesOpen, setEntriesOpen] = useState(false)
  const [entriesList, setEntriesList] = useState<AddressList | null>(null)
  const [entries, setEntries] = useState<AddressListEntry[]>([])
  const [entryValue, setEntryValue] = useState('')
  const [entryLabel, setEntryLabel] = useState('')
  const [importText, setImportText] = useState('')
  const [entriesBusy, setEntriesBusy] = useState(false)

  const [runOpen, setRunOpen] = useState(false)
  const [run, setRun] = useState<DiscoveryRun | null>(null)
  const [scanning, setScanning] = useState('')
  const [selectedHosts, setSelectedHosts] = useState<string[]>([])
  const [applying, setApplying] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const [n, l, m, a, p, pr] = await Promise.all([
        apiGet<DiscoveryNetwork[]>('/networks'),
        apiGet<AddressList[]>('/address-lists'),
        apiGet<MapInfo[]>('/maps'),
        apiGet<Agent[]>('/agents').catch(() => []),
        apiGet<SnmpProfile[]>('/snmp-profiles').catch(() => []),
        apiGet<Probe[]>('/probes').catch(() => []),
      ])
      setNetworks(n); setLists(l); setMaps(m); setAgents(a); setProfiles(p); setProbes(pr)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { void load() }, [])

  const openNetwork = (n?: DiscoveryNetwork) => {
    setError('')
    setNetworkEditing(n || null)
    setNetworkForm(n ? {
      name: n.name, cidr: n.cidr, address_list_id: n.address_list_id || '', map_id: n.map_id || '',
      agent_id: n.agent_id || '', snmp_profile_id: n.snmp_profile_id || '', probe_ids: n.probe_ids || [],
      interval_sec: n.interval_sec, mode: n.mode, name_preference: n.name_preference || 'dns_snmp_ip',
      auto_create: n.auto_create, is_enabled: n.is_enabled,
    } : { ...EMPTY_NETWORK })
    setNetworkOpen(true)
  }

  const saveNetwork = async () => {
    setError(''); setSaving(true)
    try {
      if (!networkForm.name.trim()) throw new Error('El nombre es obligatorio')
      if (!networkForm.cidr.trim() && !networkForm.address_list_id) throw new Error('Indica un CIDR o una lista de direcciones')
      const payload = {
        ...networkForm,
        cidr: networkForm.cidr.trim(),
        address_list_id: networkForm.address_list_id || '',
        map_id: networkForm.map_id || '',
        agent_id: networkForm.agent_id || '',
        snmp_profile_id: networkForm.snmp_profile_id || '',
      }
      if (networkEditing) await apiSend(`/networks/${networkEditing.id}`, 'PUT', payload)
      else await apiSend('/networks', 'POST', payload)
      setNetworkOpen(false); setNetworkEditing(null)
      await load()
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const deleteNetwork = async (n: DiscoveryNetwork) => {
    if (!await confirmAction(`¿Eliminar el descubrimiento "${n.name}"?`)) return
    await apiSend(`/networks/${n.id}`, 'DELETE')
    await load()
  }

  const scanNow = async (n: DiscoveryNetwork) => {
    setScanning(n.id); setError('')
    try {
      const data = await apiSend<DiscoveryRun>(`/networks/${n.id}/scan`, 'POST')
      setRun(data)
      setSelectedHosts((data.results || []).filter(h => h.alive && !h.existing_device_id).map(h => h.ip))
      setRunOpen(true)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally { setScanning('') }
  }

  const applyRun = async () => {
    if (!run || !selectedHosts.length) return
    setApplying(true); setError('')
    try {
      const data = await apiSend<{ created: number }>(`/networks/${run.network_id}/apply`, 'POST', {
        run_id: run.id, ips: selectedHosts,
      })
      setRunOpen(false)
      await load()
      alert(`${data.created} dispositivos creados`)
    } catch (e) { setError((e as Error).message) } finally { setApplying(false) }
  }

  const openList = (l?: AddressList) => {
    setError('')
    setListEditing(l || null)
    setListForm(l ? { name: l.name, description: l.description, color: l.color, kind: l.kind } : { name: '', description: '', color: '#0f766e', kind: 'allow' })
    setListOpen(true)
  }

  const saveList = async () => {
    setError(''); setSaving(true)
    try {
      if (!listForm.name.trim()) throw new Error('El nombre es obligatorio')
      if (listEditing) await apiSend(`/address-lists/${listEditing.id}`, 'PUT', listForm)
      else await apiSend('/address-lists', 'POST', listForm)
      setListOpen(false); setListEditing(null)
      await load()
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const deleteList = async (l: AddressList) => {
    if (!await confirmAction(`¿Eliminar la lista "${l.name}"?`)) return
    await apiSend(`/address-lists/${l.id}`, 'DELETE')
    await load()
  }

  const openEntries = async (l: AddressList) => {
    setError(''); setEntriesList(l); setImportText(''); setEntryValue(''); setEntryLabel('')
    const data = await apiGet<AddressList>(`/address-lists/${l.id}`)
    setEntries(data.entries || [])
    setEntriesOpen(true)
  }

  const addEntry = async () => {
    if (!entriesList || !entryValue.trim()) return
    setEntriesBusy(true); setError('')
    try {
      await apiSend(`/address-lists/${entriesList.id}/entries`, 'POST', { value: entryValue.trim(), label: entryLabel.trim() })
      setEntryValue(''); setEntryLabel('')
      const data = await apiGet<AddressList>(`/address-lists/${entriesList.id}`)
      setEntries(data.entries || [])
      await load()
    } catch (e) { setError((e as Error).message) } finally { setEntriesBusy(false) }
  }

  const removeEntry = async (entry: AddressListEntry) => {
    if (!entriesList) return
    await apiSend(`/address-lists/${entriesList.id}/entries/${entry.id}`, 'DELETE')
    setEntries(entries.filter(e => e.id !== entry.id))
    await load()
  }

  const importEntries = async () => {
    if (!entriesList || !importText.trim()) return
    setEntriesBusy(true); setError('')
    try {
      const data = await apiSend<{ imported: number }>(`/address-lists/${entriesList.id}/import`, 'POST', { text: importText })
      const full = await apiGet<AddressList>(`/address-lists/${entriesList.id}`)
      setEntries(full.entries || [])
      setImportText('')
      await load()
      alert(`${data.imported} entradas importadas`)
    } catch (e) { setError((e as Error).message) } finally { setEntriesBusy(false) }
  }

  const exportEntries = () => {
    const text = entries.map(e => `${e.value}${e.label ? `,${e.label}` : ''}`).join('\n')
    const blob = new Blob([text], { type: 'text/csv;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${entriesList?.name || 'lista'}.csv`
    a.click()
    URL.revokeObjectURL(url)
  }

  const exportJSON = () => {
    const payload = { format: 'netmonitor-address-list/v1', name: entriesList?.name || '', kind: entriesList?.kind || 'allow', entries }
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${entriesList?.name || 'lista'}.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  const aliveHosts = useMemo(() => (run?.results || []).filter(h => h.alive), [run])

  if (loading) return <div className="page-center"><Spinner /></div>

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Descubrimiento y direcciones</span>
          <h1>Redes y listas</h1>
          <p className="page-subtitle">Descubrimiento programado, desde agentes remotos y listas de IP/CIDR reutilizables.</p>
        </div>
        <div className="page-head-actions">
          {tab === 'networks'
            ? <Button variant="primary" onClick={() => openNetwork()}><Plus size={15} /> Nueva red</Button>
            : <Button variant="primary" onClick={() => openList()}><Plus size={15} /> Nueva lista</Button>}
        </div>
      </div>

      <div className="segmented page-segmented">
        <button className={tab === 'networks' ? 'active' : ''} onClick={() => setTab('networks')}><Radar size={15} /> Descubrimiento <span>{networks.length}</span></button>
        <button className={tab === 'lists' ? 'active' : ''} onClick={() => setTab('lists')}><ListChecks size={15} /> Listas de direcciones <span>{lists.length}</span></button>
      </div>

      {error && !networkOpen && !listOpen && !entriesOpen && <div className="form-error">{error}</div>}

      {tab === 'networks' && (
        <div className="network-grid">
          {networks.map(n => (
            <div className={`network-card ${n.last_status}`} key={n.id}>
              <div className="network-card-head">
                <div className={`settings-icon ${n.last_status === 'error' ? 'danger' : ''}`}><Network size={18} /></div>
                <div>
                  <b>{n.name}</b>
                  <span>{n.cidr || n.address_list_name || 'Sin rango'} · {n.mode === 'reliable' ? 'Reliable' : 'Fast'}</span>
                </div>
                <span className={`builtin-pill ${n.is_enabled ? '' : 'muted-pill'}`}>{n.is_enabled ? 'activo' : 'pausado'}</span>
              </div>
              <div className="network-meta">
                <span><Clock3 size={12} /> cada {formatDuration(n.interval_sec)}</span>
                <span><Server size={12} /> {n.agent_name || 'core'}</span>
                {n.map_name && <span>mapa: {n.map_name}</span>}
                {n.auto_create && <span className="unit-chip">alta automática</span>}
              </div>
              <div className="network-stats">
                <div><b>{n.last_scanned}</b><span>escaneadas</span></div>
                <div><b className="ok">{n.last_alive}</b><span>vivas</span></div>
                <div><b className="violet">{n.last_created}</b><span>creadas</span></div>
                <div><b>{futureLabel(n.next_scan_at)}</b><span>próximo</span></div>
              </div>
              {n.last_error && <div className="network-error">{n.last_error}</div>}
              <div className="network-card-actions">
                <Button variant="primary" size="sm" onClick={() => scanNow(n)} disabled={scanning === n.id}>
                  {scanning === n.id ? <RefreshCcw size={13} className="spin" /> : <Play size={13} />} {scanning === n.id ? 'Escaneando…' : 'Escanear ahora'}
                </Button>
                <Button variant="secondary" size="sm" onClick={() => openNetwork(n)}><Pencil size={13} /> Editar</Button>
                <Button variant="danger" size="sm" onClick={() => deleteNetwork(n)}><Trash2 size={13} /></Button>
              </div>
            </div>
          ))}
          {!networks.length && <EmptyState title="Sin descubrimientos programados" message="Crea una red con CIDR, intervalo y agente para descubrir equipos automáticamente." icon={<Radar size={24} />} />}
        </div>
      )}

      {tab === 'lists' && (
        <div className="network-grid">
          {lists.map(l => (
            <div className="network-card" key={l.id}>
              <div className="network-card-head">
                <div className={`settings-icon ${l.kind === 'block' ? 'danger' : ''}`}>{l.kind === 'block' ? <ShieldBan size={18} /> : <ListChecks size={18} />}</div>
                <div><b>{l.name}</b><span>{l.entry_count} entradas · {l.kind === 'block' ? 'bloqueo' : 'permitida'}</span></div>
                <span className="builtin-pill" style={{ background: `${l.color}18`, color: l.color }}>{l.kind}</span>
              </div>
              <p className="network-description">{l.description || 'Sin descripción.'}</p>
              <div className="network-card-actions">
                <Button variant="primary" size="sm" onClick={() => openEntries(l)}><ListChecks size={13} /> Ver entradas</Button>
                <Button variant="secondary" size="sm" onClick={() => openList(l)}><Pencil size={13} /> Editar</Button>
                <Button variant="danger" size="sm" onClick={() => deleteList(l)}><Trash2 size={13} /></Button>
              </div>
            </div>
          ))}
          {!lists.length && <EmptyState title="Sin listas de direcciones" message="Crea listas para reutilizarlas en descubrimiento, mantenimiento y reglas." icon={<ListChecks size={24} />} />}
        </div>
      )}

      <Modal open={networkOpen} onClose={() => { setNetworkOpen(false); setNetworkEditing(null) }} title={networkEditing ? 'Editar descubrimiento' : 'Nuevo descubrimiento'} subtitle="Barrido programado desde el core o un agente remoto." width={760}
        footer={<><Button variant="ghost" onClick={() => { setNetworkOpen(false); setNetworkEditing(null) }}>Cancelar</Button><Button variant="primary" onClick={saveNetwork} disabled={saving}>{saving ? 'Guardando…' : 'Guardar red'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={networkForm.name} onChange={e => setNetworkForm({ ...networkForm, name: e.target.value })} placeholder="Sede Norte · LAN" /></Field>
          <Field label="CIDR" hint="Vacío si usas una lista de direcciones."><input className="input mono" value={networkForm.cidr} onChange={e => setNetworkForm({ ...networkForm, cidr: e.target.value })} placeholder="192.0.2.0/24" /></Field>
          <Field label="Lista de direcciones" hint="Se combina con el CIDR si ambos están definidos.">
            <select className="input" value={networkForm.address_list_id} onChange={e => setNetworkForm({ ...networkForm, address_list_id: e.target.value })}>
              <option value="">— Sin lista</option>
              {lists.map(l => <option key={l.id} value={l.id}>{l.name} ({l.entry_count})</option>)}
            </select>
          </Field>
          <Field label="Mapa destino"><select className="input" value={networkForm.map_id} onChange={e => setNetworkForm({ ...networkForm, map_id: e.target.value })}><option value="">— No añadir al mapa</option>{maps.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}</select></Field>
          <Field label="Agente" hint="Vacío = ejecutar desde el core."><select className="input" value={networkForm.agent_id} onChange={e => setNetworkForm({ ...networkForm, agent_id: e.target.value })}><option value="">— Core</option>{agents.map(a => <option key={a.id} value={a.id}>{a.name}{a.online ? ' · online' : ' · offline'}</option>)}</select></Field>
          <Field label="Perfil SNMP" hint="Para nombre sysName, sysDescr y sysObjectID."><select className="input" value={networkForm.snmp_profile_id} onChange={e => setNetworkForm({ ...networkForm, snmp_profile_id: e.target.value })}><option value="">— Sin SNMP</option>{profiles.map(p => <option key={p.id} value={p.id}>{p.name} · {p.version}</option>)}</select></Field>
          <Field label="Intervalo (segundos)"><input className="input" type="number" min={30} value={networkForm.interval_sec} onChange={e => setNetworkForm({ ...networkForm, interval_sec: Number(e.target.value) })} /></Field>
          <Field label="Modo"><select className="input" value={networkForm.mode} onChange={e => setNetworkForm({ ...networkForm, mode: e.target.value as 'fast' | 'reliable' })}><option value="fast">Rápido · 1 paquete</option><option value="reliable">Reliable · 3 paquetes + reintento SNMP</option></select></Field>
          <Field label="Nombre preferido" hint="Orden fijo de resolución."><input className="input" value="DNS → SNMP → IP" disabled /></Field>
          <div>
            <label className="check-label"><input type="checkbox" checked={networkForm.auto_create} onChange={e => setNetworkForm({ ...networkForm, auto_create: e.target.checked })} /> Alta automática de equipos</label>
            <label className="check-label"><input type="checkbox" checked={networkForm.is_enabled} onChange={e => setNetworkForm({ ...networkForm, is_enabled: e.target.checked })} /> Descubrimiento activo</label>
          </div>
        </div>
        <Field label={`Sondas por defecto (${networkForm.probe_ids.length})`} hint="Se asignan a los dispositivos creados por este descubrimiento.">
          <div className="parent-picker" style={{ maxHeight: 180, overflowY: 'auto' }}>
            {probes.map(p => (
              <label key={p.id} className={`parent-option ${networkForm.probe_ids.includes(p.id) ? 'active' : ''}`}>
                <input type="checkbox" checked={networkForm.probe_ids.includes(p.id)}
                  onChange={e => setNetworkForm({ ...networkForm, probe_ids: e.target.checked ? [...networkForm.probe_ids, p.id] : networkForm.probe_ids.filter(id => id !== p.id) })} />
                <span>{p.name}</span><small>{p.probe_type}</small>
              </label>
            ))}
          </div>
        </Field>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={runOpen} onClose={() => setRunOpen(false)} title="Resultado del descubrimiento" subtitle={run ? `${aliveHosts.length} hosts vivos · ${run.scanned_count} escaneados` : ''} width={820}
        footer={<><Button variant="ghost" onClick={() => setRunOpen(false)}>Cerrar</Button><Button variant="primary" onClick={applyRun} disabled={applying || !selectedHosts.length}>{applying ? 'Aplicando…' : `Añadir ${selectedHosts.length} equipos`}</Button></>}>
        {error && <div className="form-error">{error}</div>}
        <div className="host-table">
          <div className="host-row head"><span /><span>IP</span><span>Nombre</span><span>Origen</span><span>SNMP</span><span>Latencia</span></div>
          {aliveHosts.map(h => (
            <label className={`host-row ${h.existing_device_id ? 'existing' : ''}`} key={h.ip}>
              <input type="checkbox" disabled={!!h.existing_device_id} checked={selectedHosts.includes(h.ip)}
                onChange={e => setSelectedHosts(e.target.checked ? [...selectedHosts, h.ip] : selectedHosts.filter(ip => ip !== h.ip))} />
              <span className="mono">{h.ip}</span>
              <span>{h.name || '—'} {h.existing_device_id && <small className="muted">· ya existe</small>}</span>
              <span className="host-source">{h.name_source || 'ip'}</span>
              <span className="host-snmp">{h.sys_descr ? h.sys_descr.slice(0, 38) : '—'}</span>
              <span className="mono">{Number(h.latency_ms || 0).toFixed(1)} ms</span>
            </label>
          ))}
          {!aliveHosts.length && <div className="walk-empty">Sin hosts vivos en este barrido.</div>}
        </div>
      </Modal>

      <Modal open={listOpen} onClose={() => { setListOpen(false); setListEditing(null) }} title={listEditing ? 'Editar lista' : 'Nueva lista de direcciones'} subtitle="IPs o CIDR reutilizables en descubrimiento, reglas y mantenimiento." width={620}
        footer={<><Button variant="ghost" onClick={() => { setListOpen(false); setListEditing(null) }}>Cancelar</Button><Button variant="primary" onClick={saveList} disabled={saving}>{saving ? 'Guardando…' : 'Guardar lista'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={listForm.name} onChange={e => setListForm({ ...listForm, name: e.target.value })} placeholder="Clientes VLAN 30" /></Field>
          <Field label="Uso"><select className="input" value={listForm.kind} onChange={e => setListForm({ ...listForm, kind: e.target.value })}><option value="allow">Lista operativa</option><option value="block">Blocklist · silenciar avisos</option></select></Field>
          <Field label="Color"><input className="input color-input" type="color" value={listForm.color} onChange={e => setListForm({ ...listForm, color: e.target.value })} /></Field>
        </div>
        <Field label="Descripción"><input className="input" value={listForm.description} onChange={e => setListForm({ ...listForm, description: e.target.value })} /></Field>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={entriesOpen} onClose={() => setEntriesOpen(false)} title={`Entradas · ${entriesList?.name || ''}`} subtitle="Una IP/CIDR por línea. Admite valor, etiqueta." width={760}>
        <div className="entry-add-row">
          <input className="input mono" value={entryValue} onChange={e => setEntryValue(e.target.value)} placeholder="192.0.2.0/24 o 192.0.2.1" />
          <input className="input" value={entryLabel} onChange={e => setEntryLabel(e.target.value)} placeholder="Etiqueta opcional" />
          <Button variant="secondary" onClick={addEntry} disabled={entriesBusy || !entryValue.trim()}><Plus size={14} /> Añadir</Button>
        </div>
        <div className="entry-toolbar">
          <Button variant="secondary" size="sm" onClick={exportEntries}><Download size={13} /> Exportar CSV</Button>
          <Button variant="secondary" size="sm" onClick={exportJSON}><Download size={13} /> Exportar JSON</Button>
          <label className="btn btn-secondary btn-sm"><Upload size={13} /> Importar CSV/JSON<input type="file" accept=".csv,.txt,.json" hidden onChange={async e => {
            const file = e.target.files?.[0]; e.target.value = ''
            if (!file) return
            const text = await file.text()
            if (file.name.endsWith('.json')) {
              try {
                const parsed = JSON.parse(text)
                if (Array.isArray(parsed.entries)) setImportText(parsed.entries.map((x: any) => `${x.value}${x.label ? `,${x.label}` : ''}`).join('\n'))
                else setImportText(text)
              } catch { setImportText(text) }
            } else setImportText(text)
          }} /></label>
        </div>
        {importText && (
          <div className="entry-import">
            <textarea className="input textarea mono" rows={4} value={importText} onChange={e => setImportText(e.target.value)} />
            <Button variant="primary" size="sm" onClick={importEntries} disabled={entriesBusy}><Check size={13} /> Importar {importText.split('\n').filter(Boolean).length} líneas</Button>
          </div>
        )}
        <div className="entry-list">
          {entries.map(e => (
            <div className="entry-row" key={e.id}>
              <span className="mono">{e.value}</span>
              <span>{e.label || '—'}</span>
              <Button variant="danger" size="sm" onClick={() => removeEntry(e)}><Trash2 size={12} /></Button>
            </div>
          ))}
          {!entries.length && <div className="walk-empty">Sin entradas todavía.</div>}
        </div>
        {error && <div className="form-error">{error}</div>}
      </Modal>
    </div>
  )
}
