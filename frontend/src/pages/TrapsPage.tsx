import { useEffect, useMemo, useState } from 'react'
import {
  AlertTriangle,
  BellRing,
  BookOpen,
  FlaskConical,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Siren,
  Trash2,
} from 'lucide-react'
import { apiGet, apiSend, getUser } from '../api'
import { confirmAction } from '../confirm'
import { useRealtime } from '../realtime'
import type { Device, MapInfo, MIBNotification, NotificationChannel, TrapEvent, TrapRule } from '../types'
import { Button, EmptyState, Field, Modal, Spinner, StatusBadge, timeAgo } from '../ui'

type Tab = 'feed' | 'rules' | 'mibs' | 'settings'

const EMPTY_RULE = {
  name: '', priority: 100, match_oid: '', match_name: '', severity: 'warning' as TrapRule['severity'],
  event_type: 'snmp_trap', message_template: '', filter_device_ids: [] as string[], filter_tags: [] as string[],
  filter_map_ids: [] as string[], channel_ids: [] as string[], notify: true, suppress: false, is_enabled: true,
}

export default function TrapsPage() {
  const isAdmin = getUser()?.role === 'admin'
  const [tab, setTab] = useState<Tab>('feed')
  const [traps, setTraps] = useState<TrapEvent[]>([])
  const [rules, setRules] = useState<TrapRule[]>([])
  const [notifications, setNotifications] = useState<MIBNotification[]>([])
  const [devices, setDevices] = useState<Device[]>([])
  const [maps, setMaps] = useState<MapInfo[]>([])
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [community, setCommunity] = useState('public')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [severityFilter, setSeverityFilter] = useState('')
  const [mibQuery, setMibQuery] = useState('')

  const [detail, setDetail] = useState<TrapEvent | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)
  const [ruleOpen, setRuleOpen] = useState(false)
  const [editingRule, setEditingRule] = useState<TrapRule | null>(null)
  const [ruleForm, setRuleForm] = useState({ ...EMPTY_RULE })
  const [testOpen, setTestOpen] = useState(false)
  const [testDevice, setTestDevice] = useState('')
  const [testOID, setTestOID] = useState('1.3.6.1.6.3.1.1.5.3')

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [t, r, n, d, m, c, settings] = await Promise.all([
        apiGet<TrapEvent[]>('/traps?limit=300'),
        apiGet<TrapRule[]>('/traps/rules'),
        apiGet<MIBNotification[]>('/traps/notifications'),
        apiGet<Device[]>('/devices').catch(() => []),
        apiGet<MapInfo[]>('/maps').catch(() => []),
        apiGet<NotificationChannel[]>('/notification-channels').catch(() => []),
        apiGet<{ community: string; requires_restart: boolean }>('/traps/settings').catch(() => ({ community: 'public', requires_restart: true })),
      ])
      setTraps(t); setRules(r); setNotifications(n); setDevices(d); setMaps(m); setChannels(c); setCommunity(settings.community)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { void load() }, [])
  const { refreshKey } = useRealtime()
  useEffect(() => {
    if (refreshKey) void load()
  }, [refreshKey])

  const flash = (msg: string) => { setNotice(msg); window.setTimeout(() => setNotice(''), 4000) }
  const filteredTraps = useMemo(() => severityFilter ? traps.filter(t => t.severity === severityFilter) : traps, [traps, severityFilter])
  const filteredMIBs = useMemo(() => {
    const q = mibQuery.trim().toLowerCase()
    if (!q) return notifications
    return notifications.filter(n => [n.name, n.oid, n.module, n.description].some(v => (v || '').toLowerCase().includes(q)))
  }, [notifications, mibQuery])

  const openDetail = async (trap: TrapEvent) => {
    try {
      const full = await apiGet<TrapEvent>(`/traps/${trap.id}`)
      setDetail(full)
      setDetailOpen(true)
    } catch (e) { setError((e as Error).message) }
  }

  const openRule = (rule?: TrapRule) => {
    setEditingRule(rule || null)
    setRuleForm(rule ? {
      name: rule.name, priority: rule.priority, match_oid: rule.match_oid, match_name: rule.match_name,
      severity: rule.severity, event_type: rule.event_type, message_template: rule.message_template,
      filter_device_ids: rule.filter_device_ids || [], filter_tags: rule.filter_tags || [],
      filter_map_ids: rule.filter_map_ids || [], channel_ids: rule.channel_ids || [],
      notify: rule.notify, suppress: rule.suppress, is_enabled: rule.is_enabled,
    } : { ...EMPTY_RULE })
    setError('')
    setRuleOpen(true)
  }

  const saveRule = async () => {
    setBusy('rule')
    setError('')
    try {
      if (!ruleForm.name.trim()) throw new Error('El nombre es obligatorio')
      if (editingRule) await apiSend(`/traps/rules/${editingRule.id}`, 'PUT', ruleForm)
      else await apiSend('/traps/rules', 'POST', ruleForm)
      setRuleOpen(false)
      await load()
      flash('Regla guardada')
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  const deleteRule = async (rule: TrapRule) => {
    if (!await confirmAction(`¿Eliminar la regla de trap "${rule.name}"?`)) return
    try { await apiSend(`/traps/rules/${rule.id}`, 'DELETE'); await load() } catch (e) { setError((e as Error).message) }
  }

  const runTest = async () => {
    setBusy('test')
    setError('')
    try {
      await apiSend('/traps/test', 'POST', { device_id: testDevice, trap_oid: testOID })
      setTestOpen(false)
      setTab('feed')
      await load()
      flash('Trap de prueba procesado. Revisa el feed.')
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  const saveCommunity = async () => {
    setBusy('settings')
    try {
      await apiSend('/traps/settings', 'PUT', { community })
      flash('Comunidad guardada. Reinicia el core para que el listener la use.')
    } catch (e) { setError((e as Error).message) } finally { setBusy('') }
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Fase 3 · Eventos</span>
          <h1>Traps SNMP</h1>
          <p className="page-subtitle">Decodificación con MIB importadas, reglas propias y supresión por causa raíz.</p>
        </div>
        <div className="page-head-actions">
          {isAdmin && <Button variant="secondary" onClick={() => setTestOpen(true)}><FlaskConical size={14} /> Probar trap</Button>}
          <Button variant="secondary" onClick={() => void load()}><RefreshCw size={14} /> Actualizar</Button>
          {isAdmin && tab === 'rules' && <Button variant="primary" onClick={() => openRule()}><Plus size={14} /> Regla</Button>}
        </div>
      </div>

      <div className="page-tabs">
        <button className={tab === 'feed' ? 'active' : ''} onClick={() => setTab('feed')}><Siren size={15} /> Feed <span>{traps.length}</span></button>
        <button className={tab === 'rules' ? 'active' : ''} onClick={() => setTab('rules')}><BellRing size={15} /> Reglas <span>{rules.length}</span></button>
        <button className={tab === 'mibs' ? 'active' : ''} onClick={() => setTab('mibs')}><BookOpen size={15} /> MIBs <span>{notifications.length}</span></button>
        {isAdmin && <button className={tab === 'settings' ? 'active' : ''} onClick={() => setTab('settings')}>Receptor</button>}
      </div>

      {notice && <div className="notice-ok">{notice}</div>}
      {error && <div className="form-error"><AlertTriangle size={14} /> {error}</div>}

      {loading ? <div className="page-center"><Spinner /></div> : <>
        {tab === 'feed' && (
          <div className="table-card">
            <div className="table-toolbar">
              <div className="filter-chips">
                {['', 'critical', 'warning', 'info', 'ok'].map(sev => <button key={sev} className={`chip ${severityFilter === sev ? 'active' : ''}`} onClick={() => setSeverityFilter(sev)}>{sev ? sev.toUpperCase() : 'Todos'}</button>)}
              </div>
              <span className="muted small">{filteredTraps.filter(t => t.suppressed).length} suprimidos por correlación</span>
            </div>
            <div className="data-table traps-table">
              <div className="data-row head"><span>Severidad</span><span>Trap</span><span>Origen</span><span>Mensaje</span><span>Recibido</span><span /></div>
              {filteredTraps.map(t => (
                <div className="data-row" key={t.id} onClick={() => void openDetail(t)} style={{ cursor: 'pointer' }}>
                  <span><StatusBadge status={t.severity === 'critical' ? 'down' : t.severity === 'warning' ? 'warning' : t.severity === 'ok' ? 'up' : 'unknown'} text={t.severity} /></span>
                  <span className="cell-main"><b>{t.trap_name || 'Trap'}</b><small className="mono">{t.trap_oid}</small></span>
                  <span className="cell-main"><b>{t.device_name || t.source_ip}</b><small className="mono">{t.source_ip} · {t.version}</small></span>
                  <span className="cell-message">{t.message}{t.suppressed && <small className="suppressed-pill">Suprimido · causa: {t.root_cause || 'aguas arriba'}</small>}{t.matched_rule && <small className="muted">Regla: {t.matched_rule}</small>}</span>
                  <span className="muted small">{timeAgo(t.at)}</span>
                  <span className="mono small">#{t.id}</span>
                </div>
              ))}
              {!filteredTraps.length && <EmptyState title="Sin traps recibidos" message="Envía un trap SNMP v1/v2c al puerto 162 o utiliza Probar trap para validar el flujo." icon={<Siren size={24} />} />}
            </div>
          </div>
        )}

        {tab === 'rules' && (
          <div className="table-card">
            <div className="data-table trap-rules-table">
              <div className="data-row head"><span>Prioridad</span><span>Regla</span><span>Coincidencia</span><span>Acción</span><span>Canales</span><span /></div>
              {rules.map(r => (
                <div className="data-row" key={r.id}>
                  <span className="mono">{r.priority}</span>
                  <span className="cell-main"><b>{r.name}</b><small>{r.is_enabled ? 'Activa' : 'Pausada'}</small></span>
                  <span className="cell-main"><b className="mono small">{r.match_oid || 'cualquier OID'}</b><small>{r.match_name && `nombre ~ ${r.match_name}`}</small></span>
                  <span><StatusBadge status={r.severity === 'critical' ? 'down' : r.severity === 'info' || r.severity === 'ok' ? 'up' : 'warning'} text={r.severity} />{r.suppress && <small className="suppressed-pill">Suprime evento</small>}</span>
                  <span className="muted small">{r.notify ? (r.channel_ids.length ? `${r.channel_ids.length} canales` : 'Reglas de alerta') : 'Solo evento'}</span>
                  <span className="row-actions">{isAdmin && <><Button size="sm" variant="ghost" onClick={() => openRule(r)}><Pencil size={13} /> Editar</Button><Button size="sm" variant="ghost" onClick={() => void deleteRule(r)}><Trash2 size={13} /></Button></>}</span>
                </div>
              ))}
              {!rules.length && <EmptyState title="Sin reglas de traps" message="Las reglas convierten OIDs concretos en eventos con severidad, mensaje y notificación." icon={<BellRing size={24} />} />}
            </div>
          </div>
        )}

        {tab === 'mibs' && (
          <div className="table-card">
            <div className="table-toolbar">
              <div className="search-box" style={{ maxWidth: 360 }}><Search size={14} /><input value={mibQuery} onChange={e => setMibQuery(e.target.value)} placeholder="Buscar nombre, OID o módulo…" /></div>
              <span className="muted small">Importa MIBs desde Ajustes → MIBs para decodificar nombres y varbinds.</span>
            </div>
            <div className="data-table mib-notifications-table">
              <div className="data-row head"><span>Módulo</span><span>Notificación</span><span>OID</span><span>Objetos</span></div>
              {filteredMIBs.map(n => (
                <div className="data-row" key={n.module + n.oid}>
                  <span className="mono small">{n.module}</span>
                  <span className="cell-main"><b>{n.name}</b><small>{n.description || 'Sin descripción'}</small></span>
                  <span className="mono small">{n.oid}</span>
                  <span className="muted small">{n.objects?.join(', ') || '—'}</span>
                </div>
              ))}
              {!filteredMIBs.length && <EmptyState title="Sin NOTIFICATION-TYPE importados" message="Importa MIBs que contengan NOTIFICATION-TYPE para enriquecer los traps con nombres y descripciones." icon={<BookOpen size={24} />} />}
            </div>
          </div>
        )}

        {tab === 'settings' && (
          <div className="table-card">
            <div className="settings-block">
              <div><h3>Receptor de traps</h3><p className="muted small">Escucha en <b className="mono">0.0.0.0:1620/udp</b> (publicado como 162/udp). Compatible con SNMP v1 y v2c. La comunidad se lee al arrancar el listener.</p></div>
              <Field label="Comunidad SNMP de traps"><input className="input mono" value={community} onChange={e => setCommunity(e.target.value)} /></Field>
              <Button variant="primary" onClick={() => void saveCommunity()} disabled={busy === 'settings'}>Guardar</Button>
              <div className="warning-box"><AlertTriangle size={16} /><div><b>Requiere reiniciar el core</b><span>El listener UDP mantiene la comunidad en memoria. Tras guardar, reinicia NetMonitor para aplicarla.</span></div></div>
            </div>
          </div>
        )}
      </>}

      <Modal open={detailOpen} onClose={() => setDetailOpen(false)} title={detail?.trap_name || 'Trap SNMP'} subtitle={detail ? `${detail.source_ip} · ${detail.version} · ${new Date(detail.at).toLocaleString('es-ES')}` : ''} width={760} footer={<Button variant="ghost" onClick={() => setDetailOpen(false)}>Cerrar</Button>}>
        {detail && <>
          <div className="kv-inline"><span>OID</span><b className="mono">{detail.trap_oid}</b><span>Severidad</span><b>{detail.severity}</b><span>Regla</span><b>{detail.matched_rule || 'Ninguna'}</b><span>Causa raíz</span><b>{detail.root_cause || '—'}</b></div>
          <p><b>{detail.message}</b></p>
          {detail.suppressed && <div className="warning-box"><AlertTriangle size={15} /><div><b>Evento suprimido por correlación</b><span>El equipo está aguas abajo de {detail.root_cause || 'una causa raíz activa'}; el trap se conserva para forensia.</span></div></div>}
          <h4 className="section-mini-title">Varbinds decodificados</h4>
          <div className="data-table trap-varbind-table">
            <div className="data-row head"><span>OID</span><span>Nombre</span><span>Tipo</span><span>Valor</span></div>
            {(detail.variables || []).map(v => <div className="data-row" key={v.oid}><span className="mono small">{v.oid}</span><span>{v.name}</span><span className="muted small">{v.type || '—'}</span><span className="mono small">{v.value}</span></div>)}
          </div>
        </>}
      </Modal>

      <Modal open={ruleOpen} onClose={() => setRuleOpen(false)} title={editingRule ? 'Editar regla de trap' : 'Nueva regla de trap'} subtitle="Convierte un OID o familia de OIDs en un evento accionable." width={720} footer={<><Button variant="ghost" onClick={() => setRuleOpen(false)}>Cancelar</Button><Button variant="primary" onClick={() => void saveRule()} disabled={busy === 'rule'}>{busy === 'rule' ? 'Guardando…' : 'Guardar'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={ruleForm.name} onChange={e => setRuleForm({ ...ruleForm, name: e.target.value })} /></Field>
          <Field label="Prioridad" hint="Menor número = mayor prioridad."><input className="input mono" type="number" value={ruleForm.priority} onChange={e => setRuleForm({ ...ruleForm, priority: Number(e.target.value) })} /></Field>
          <Field label="OID del trap" hint="Exacto o prefijo terminado en *."><input className="input mono" value={ruleForm.match_oid} onChange={e => setRuleForm({ ...ruleForm, match_oid: e.target.value })} placeholder="1.3.6.1.6.3.1.1.5.3" /></Field>
          <Field label="Nombre contiene"><input className="input" value={ruleForm.match_name} onChange={e => setRuleForm({ ...ruleForm, match_name: e.target.value })} placeholder="linkDown" /></Field>
          <Field label="Severidad"><select className="input" value={ruleForm.severity} onChange={e => setRuleForm({ ...ruleForm, severity: e.target.value as TrapRule['severity'] })}>{['info', 'ok', 'warning', 'critical'].map(s => <option key={s} value={s}>{s}</option>)}</select></Field>
          <Field label="Tipo de evento"><input className="input mono" value={ruleForm.event_type} onChange={e => setRuleForm({ ...ruleForm, event_type: e.target.value })} /></Field>
        </div>
        <Field label="Mensaje" hint="Variables: {{trap_name}}, {{trap_oid}}, {{source_ip}}, {{device_name}}, {{varbind.NOMBRE}}."><textarea className="input" rows={3} value={ruleForm.message_template} onChange={e => setRuleForm({ ...ruleForm, message_template: e.target.value })} /></Field>
        <div className="form-grid two">
          <Field label="Etiquetas (separadas por coma)"><input className="input" value={ruleForm.filter_tags.join(', ')} onChange={e => setRuleForm({ ...ruleForm, filter_tags: e.target.value.split(',').map(x => x.trim()).filter(Boolean) })} /></Field>
          <Field label="Filtrar por mapa"><select className="input" value={ruleForm.filter_map_ids[0] || ''} onChange={e => setRuleForm({ ...ruleForm, filter_map_ids: e.target.value ? [e.target.value] : [] })}><option value="">Cualquier mapa</option>{maps.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}</select></Field>
          <Field label="Filtrar por dispositivo"><select className="input" value={ruleForm.filter_device_ids[0] || ''} onChange={e => setRuleForm({ ...ruleForm, filter_device_ids: e.target.value ? [e.target.value] : [] })}><option value="">Cualquier dispositivo</option>{devices.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}</select></Field>
          <Field label="Canales de notificación" hint="Si no marcas ninguno, se usan las reglas de alerta con transición snmp_trap.">
            <div className="target-picker compact">{channels.map(c => <label key={c.id} className={ruleForm.channel_ids.includes(c.id) ? 'selected' : ''}><input type="checkbox" checked={ruleForm.channel_ids.includes(c.id)} onChange={e => setRuleForm({ ...ruleForm, channel_ids: e.target.checked ? [...ruleForm.channel_ids, c.id] : ruleForm.channel_ids.filter(x => x !== c.id) })} /><span><b>{c.name}</b><small>{c.channel_type}</small></span></label>)}</div>
          </Field>
        </div>
        <div className="check-row"><label><input type="checkbox" checked={ruleForm.notify} onChange={e => setRuleForm({ ...ruleForm, notify: e.target.checked })} /> Notificar</label><label><input type="checkbox" checked={ruleForm.suppress} onChange={e => setRuleForm({ ...ruleForm, suppress: e.target.checked })} /> Suprimir evento</label><label><input type="checkbox" checked={ruleForm.is_enabled} onChange={e => setRuleForm({ ...ruleForm, is_enabled: e.target.checked })} /> Activa</label></div>
      </Modal>

      <Modal open={testOpen} onClose={() => setTestOpen(false)} title="Probar trap SNMP" subtitle="Inyecta un trap sintético por el mismo decoder, reglas y correlación." width={560} footer={<><Button variant="ghost" onClick={() => setTestOpen(false)}>Cancelar</Button><Button variant="primary" onClick={() => void runTest()} disabled={busy === 'test'}>{busy === 'test' ? 'Procesando…' : 'Enviar trap'}</Button></>}>
        <Field label="Dispositivo de origen"><select className="input" value={testDevice} onChange={e => setTestDevice(e.target.value)}><option value="">Sin dispositivo (127.0.0.1)</option>{devices.map(d => <option key={d.id} value={d.id}>{d.name} · {d.primary_ip}</option>)}</select></Field>
        <Field label="OID del trap"><input className="input mono" value={testOID} onChange={e => setTestOID(e.target.value)} /></Field>
        <div className="muted small">Ejemplos: linkDown <span className="mono">1.3.6.1.6.3.1.1.5.3</span>, linkUp <span className="mono">1.3.6.1.6.3.1.1.5.4</span>, coldStart <span className="mono">1.3.6.1.6.3.1.1.5.1</span>.</div>
      </Modal>
    </div>
  )
}
