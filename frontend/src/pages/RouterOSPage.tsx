import { useEffect, useMemo, useState } from 'react'
import { Navigate } from 'react-router-dom'
import {
  AlertTriangle,
  CheckCircle2,
  FileCode2,
  KeyRound,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Router,
  Rocket,
  ShieldCheck,
  Terminal,
  Trash2,
  X,
} from 'lucide-react'
import { apiGet, apiSend, getUser } from '../api'
import { confirmAction } from '../confirm'
import type {
  RouterOSCredential,
  RouterOSDevice,
  RouterOSInventory,
  RouterOSJob,
  RouterOSPlanItem,
  RouterOSTemplate,
} from '../types'
import { Button, EmptyState, Field, Modal, Spinner, StatusBadge, timeAgo } from '../ui'

type Tab = 'inventory' | 'templates' | 'jobs' | 'credentials'

const EMPTY_CREDENTIAL = { name: '', username: '', password: '', port: 8728, use_tls: false, insecure_tls: false, description: '' }
const EMPTY_TEMPLATE = { name: '', description: '', category: 'config', content: '', variables: '[]' }
const EMPTY_JOB = { name: '', template_id: '', content: '', target_device_ids: [] as string[], requires_approval: true, variables: '{}', notes: '' }

function jobStatus(status: string) {
  switch (status) {
    case 'completed': return { status: 'up', label: 'Completado' }
    case 'failed': return { status: 'down', label: 'Fallido' }
    case 'running': return { status: 'warning', label: 'Ejecutando' }
    case 'approved': return { status: 'acknowledged', label: 'Aprobado' }
    case 'cancelled': return { status: 'disabled', label: 'Cancelado' }
    default: return { status: 'warning', label: 'Pendiente de aprobación' }
  }
}

export default function RouterOSPage() {
  const isAdmin = getUser()?.role === 'admin'
  const [tab, setTab] = useState<Tab>('inventory')
  const [inventory, setInventory] = useState<RouterOSInventory>({ devices: [], managed: 0, total: 0 })
  const [credentials, setCredentials] = useState<RouterOSCredential[]>([])
  const [templates, setTemplates] = useState<RouterOSTemplate[]>([])
  const [jobs, setJobs] = useState<RouterOSJob[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [search, setSearch] = useState('')

  const [credentialOpen, setCredentialOpen] = useState(false)
  const [editingCredential, setEditingCredential] = useState<RouterOSCredential | null>(null)
  const [credentialForm, setCredentialForm] = useState({ ...EMPTY_CREDENTIAL })

  const [templateOpen, setTemplateOpen] = useState(false)
  const [editingTemplate, setEditingTemplate] = useState<RouterOSTemplate | null>(null)
  const [templateForm, setTemplateForm] = useState({ ...EMPTY_TEMPLATE })
  const [previewOpen, setPreviewOpen] = useState(false)
  const [previewDevice, setPreviewDevice] = useState('')
  const [previewContent, setPreviewContent] = useState('')

  const [assignOpen, setAssignOpen] = useState(false)
  const [assignDevice, setAssignDevice] = useState<RouterOSDevice | null>(null)
  const [assignCredential, setAssignCredential] = useState('')
  const [assignPort, setAssignPort] = useState(0)
  const [testResult, setTestResult] = useState<Record<string, unknown> | null>(null)

  const [jobOpen, setJobOpen] = useState(false)
  const [jobForm, setJobForm] = useState({ ...EMPTY_JOB })
  const [jobDetail, setJobDetail] = useState<RouterOSJob | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)
  const [dryRun, setDryRun] = useState<Record<string, unknown>[]>([])

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [inv, creds, tpls, jobList] = await Promise.all([
        apiGet<RouterOSInventory>('/routeros/inventory'),
        apiGet<RouterOSCredential[]>('/routeros/credentials'),
        apiGet<RouterOSTemplate[]>('/routeros/templates'),
        apiGet<RouterOSJob[]>('/routeros/jobs'),
      ])
      setInventory(inv)
      setCredentials(creds)
      setTemplates(tpls)
      setJobs(jobList)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { void load() }, [])

  const managedDevices = useMemo(() => inventory.devices.filter(d => d.is_managed), [inventory])
  const filteredDevices = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return inventory.devices
    return inventory.devices.filter(d => [d.name, d.primary_ip, d.routeros_version, d.hardware_model, d.sys_name].some(v => (v || '').toLowerCase().includes(q)))
  }, [inventory.devices, search])

  const flash = (message: string) => {
    setNotice(message)
    window.setTimeout(() => setNotice(''), 4000)
  }

  const refreshInventory = async (id?: string) => {
    setBusy(id || 'all')
    setError('')
    try {
      const results = await apiSend<Record<string, unknown>[]>('/routeros/inventory/refresh', 'POST', { device_ids: id ? [id] : [] })
      const failed = results.filter(r => !r.success).length
      flash(failed ? `${results.length - failed} equipos actualizados, ${failed} con error` : `${results.length} equipos actualizados`)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const openCredential = (credential?: RouterOSCredential) => {
    setEditingCredential(credential || null)
    setCredentialForm(credential ? {
      name: credential.name, username: credential.username, password: '', port: credential.port,
      use_tls: credential.use_tls, insecure_tls: credential.insecure_tls, description: credential.description,
    } : { ...EMPTY_CREDENTIAL })
    setError('')
    setCredentialOpen(true)
  }

  const saveCredential = async () => {
    setBusy('credential')
    setError('')
    try {
      if (!credentialForm.name.trim() || !credentialForm.username.trim()) throw new Error('Nombre y usuario son obligatorios')
      const payload = { ...credentialForm, password: credentialForm.password || undefined }
      if (editingCredential) await apiSend(`/routeros/credentials/${editingCredential.id}`, 'PUT', payload)
      else await apiSend('/routeros/credentials', 'POST', payload)
      setCredentialOpen(false)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const deleteCredential = async (credential: RouterOSCredential) => {
    if (!await confirmAction(`¿Eliminar la credencial "${credential.name}"? Los equipos asignados dejarán de estar gestionados.`)) return
    try {
      await apiSend(`/routeros/credentials/${credential.id}`, 'DELETE')
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const openAssign = (device: RouterOSDevice) => {
    setAssignDevice(device)
    setAssignCredential(device.credential_id || '')
    setAssignPort(device.api_port || 0)
    setTestResult(null)
    setError('')
    setAssignOpen(true)
  }

  const saveAssign = async () => {
    if (!assignDevice) return
    setBusy('assign')
    setError('')
    try {
      await apiSend('/routeros/assign', 'POST', { device_id: assignDevice.id, credential_id: assignCredential, api_port: assignPort })
      setAssignOpen(false)
      await load()
      flash(assignCredential ? 'Credencial asignada' : 'Equipo desasignado')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const testDevice = async (device: RouterOSDevice) => {
    setBusy('test-' + device.id)
    setError('')
    try {
      const data = await apiSend<Record<string, unknown>>('/routeros/test', 'POST', { device_id: device.id })
      setAssignDevice(device)
      setAssignCredential(device.credential_id || '')
      setAssignPort(device.api_port || 0)
      setTestResult(data)
      setAssignOpen(true)
      flash('Conexión RouterOS correcta')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const openTemplate = (template?: RouterOSTemplate) => {
    setEditingTemplate(template || null)
    setTemplateForm(template ? {
      name: template.name, description: template.description, category: template.category,
      content: template.content, variables: JSON.stringify(template.variables || [], null, 2),
    } : { ...EMPTY_TEMPLATE })
    setError('')
    setPreviewContent('')
    setTemplateOpen(true)
  }

  const saveTemplate = async () => {
    setBusy('template')
    setError('')
    try {
      if (!templateForm.name.trim() || !templateForm.content.trim()) throw new Error('Nombre y script son obligatorios')
      let variables: unknown = []
      try { variables = JSON.parse(templateForm.variables || '[]') } catch { throw new Error('Las variables deben ser JSON válido') }
      const payload = { ...templateForm, variables }
      if (editingTemplate) await apiSend(`/routeros/templates/${editingTemplate.id}`, 'PUT', payload)
      else await apiSend('/routeros/templates', 'POST', payload)
      setTemplateOpen(false)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const deleteTemplate = async (template: RouterOSTemplate) => {
    if (!await confirmAction(`¿Eliminar la plantilla "${template.name}"?`)) return
    try {
      await apiSend(`/routeros/templates/${template.id}`, 'DELETE')
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const preview = async () => {
    setBusy('preview')
    setError('')
    try {
      let variables: unknown = {}
      try {
        variables = JSON.parse(templateForm.variables || '{}')
      } catch {
        throw new Error('Las variables de ejemplo deben ser JSON válido')
      }
      const data = await apiSend<{ content: string }>('/routeros/templates/preview', 'POST', {
        content: templateForm.content, device_id: previewDevice, variables,
      })
      setPreviewContent(data.content)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const openJob = () => {
    setJobForm({ ...EMPTY_JOB, target_device_ids: [] })
    setError('')
    setJobOpen(true)
  }

  const selectTemplate = (id: string) => {
    const template = templates.find(t => t.id === id)
    setJobForm(prev => ({ ...prev, template_id: id, content: template?.content || prev.content }))
  }

  const createJob = async () => {
    setBusy('job')
    setError('')
    try {
      if (!jobForm.name.trim()) throw new Error('El nombre es obligatorio')
      if (!jobForm.target_device_ids.length) throw new Error('Selecciona al menos un equipo')
      if (!jobForm.template_id && !jobForm.content.trim()) throw new Error('Selecciona una plantilla o escribe un script')
      let variables: unknown = {}
      try { variables = JSON.parse(jobForm.variables || '{}') } catch { throw new Error('Las variables deben ser JSON válido') }
      const data = await apiSend<{ id: string; status: string; plan: RouterOSPlanItem[] }>('/routeros/jobs', 'POST', {
        name: jobForm.name, template_id: jobForm.template_id, content: jobForm.template_id ? '' : jobForm.content,
        variables, target_device_ids: jobForm.target_device_ids, requires_approval: true, notes: jobForm.notes,
      })
      setJobOpen(false)
      await load()
      flash(data.status === 'pending_approval' ? 'Trabajo creado y pendiente de aprobación' : 'Trabajo aprobado y listo para ejecutar')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const openJobDetail = async (job: RouterOSJob) => {
    setBusy('detail-' + job.id)
    setError('')
    try {
      const full = await apiGet<RouterOSJob>(`/routeros/jobs/${job.id}`)
      setJobDetail(full)
      setDryRun([])
      setDetailOpen(true)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const approveJob = async (job: RouterOSJob) => {
    if (!await confirmAction(`¿Aprobar el despliegue "${job.name}"? Después podrá ejecutarse en ${job.targets} equipos.`)) return
    setBusy('approve-' + job.id)
    try {
      await apiSend(`/routeros/jobs/${job.id}/approve`, 'POST')
      await load()
      if (jobDetail?.id === job.id) await openJobDetail(job)
      flash('Trabajo aprobado')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const runJob = async (job: RouterOSJob) => {
    if (!await confirmAction(`¿Ejecutar "${job.name}" ahora en ${job.targets} equipos? Los cambios se aplicarán de inmediato.`)) return
    setBusy('run-' + job.id)
    try {
      await apiSend(`/routeros/jobs/${job.id}/run`, 'POST')
      await load()
      flash('Ejecución iniciada')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const runDry = async (job: RouterOSJob) => {
    setBusy('dry-' + job.id)
    setError('')
    try {
      const data = await apiSend<Record<string, unknown>[]>('/routeros/jobs/' + job.id + '/dry-run', 'POST')
      setDryRun(data)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const cancelJob = async (job: RouterOSJob) => {
    if (!await confirmAction(`¿Cancelar "${job.name}"?`)) return
    try {
      await apiSend(`/routeros/jobs/${job.id}/cancel`, 'POST')
      await load()
      setDetailOpen(false)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const deleteJob = async (job: RouterOSJob) => {
    if (!await confirmAction(`¿Eliminar el registro del trabajo "${job.name}"?`)) return
    try {
      await apiSend(`/routeros/jobs/${job.id}`, 'DELETE')
      await load()
      setDetailOpen(false)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  if (!isAdmin) return <Navigate to="/maps" replace />

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Fase 3 · Gestión</span>
          <h1>RouterOS</h1>
          <p className="page-subtitle">Inventario MikroTik, plantillas revisadas y despliegues masivos con aprobación y auditoría.</p>
        </div>
        <div className="page-head-actions">
          <div className="page-stat"><b>{inventory.managed}</b><span>gestionados</span></div>
          <div className="page-stat"><b>{jobs.filter(j => j.status === 'pending_approval').length}</b><span>pendientes</span></div>
          <Button variant="secondary" onClick={() => void load()}><RefreshCw size={14} /> Actualizar</Button>
          {tab === 'templates' && <Button variant="primary" onClick={() => openTemplate()}><Plus size={14} /> Plantilla</Button>}
          {tab === 'jobs' && <Button variant="primary" onClick={openJob}><Rocket size={14} /> Nuevo despliegue</Button>}
          {tab === 'credentials' && <Button variant="primary" onClick={() => openCredential()}><Plus size={14} /> Credencial</Button>}
        </div>
      </div>

      <div className="page-tabs">
        <button className={tab === 'inventory' ? 'active' : ''} onClick={() => setTab('inventory')}><Router size={15} /> Inventario</button>
        <button className={tab === 'templates' ? 'active' : ''} onClick={() => setTab('templates')}><FileCode2 size={15} /> Plantillas</button>
        <button className={tab === 'jobs' ? 'active' : ''} onClick={() => setTab('jobs')}><Rocket size={15} /> Despliegues</button>
        <button className={tab === 'credentials' ? 'active' : ''} onClick={() => setTab('credentials')}><KeyRound size={15} /> Credenciales</button>
      </div>

      {notice && <div className="notice-ok"><CheckCircle2 size={15} /> {notice}</div>}
      {error && <div className="form-error"><AlertTriangle size={14} /> {error}</div>}

      {loading ? <div className="page-center"><Spinner /></div> : (
        <>
          {tab === 'inventory' && (
            <div className="table-card">
              <div className="table-toolbar">
                <input className="input" style={{ maxWidth: 320 }} placeholder="Buscar equipo, IP, modelo o versión…" value={search} onChange={e => setSearch(e.target.value)} />
                <Button variant="secondary" onClick={() => void refreshInventory()} disabled={busy === 'all'}>
                  <RefreshCw size={14} className={busy === 'all' ? 'spin' : ''} /> {busy === 'all' ? 'Sincronizando…' : 'Sincronizar gestionados'}
                </Button>
              </div>
              <div className="data-table routeros-inventory-table">
                <div className="data-row head"><span>Equipo</span><span>Modelo / versión</span><span>Credencial</span><span>Última sync</span><span>Estado</span><span /></div>
                {filteredDevices.map(d => (
                  <div className="data-row" key={d.id}>
                    <span className="cell-main"><b>{d.name}</b><small className="mono">{d.primary_ip} · {d.sys_name || d.device_type}</small></span>
                    <span className="cell-main"><b>{d.hardware_model || '—'}</b><small>{d.routeros_version || 'Sin versión'}</small></span>
                    <span>{d.credential_name ? <span className="cred-pill"><KeyRound size={11} /> {d.credential_name}</span> : <span className="muted small">Sin asignar</span>}</span>
                    <span className="muted small">{d.last_sync_at ? timeAgo(d.last_sync_at) : 'Nunca'}{d.last_error && <small className="text-danger">{d.last_error}</small>}</span>
                    <span><StatusBadge status={d.status} /></span>
                    <span className="row-actions">
                      <Button size="sm" variant="ghost" onClick={() => openAssign(d)}><ShieldCheck size={13} /> Asignar</Button>
                      <Button size="sm" variant="ghost" onClick={() => void testDevice(d)} disabled={!d.is_managed || busy === 'test-' + d.id}><Terminal size={13} /> Probar</Button>
                      <Button size="sm" variant="ghost" onClick={() => void refreshInventory(d.id)} disabled={!d.is_managed || busy === d.id}><RefreshCw size={13} className={busy === d.id ? 'spin' : ''} /> Sync</Button>
                    </span>
                  </div>
                ))}
                {!filteredDevices.length && <EmptyState title="Sin equipos RouterOS" message="Asigna una credencial RouterOS a un equipo del inventario para gestionarlo." icon={<Router size={24} />} />}
              </div>
            </div>
          )}

          {tab === 'templates' && (
            <div className="routeros-template-grid">
              {templates.map(t => (
                <div className="routeros-template-card" key={t.id}>
                  <div className="routeros-template-head">
                    <div><b>{t.name}</b><small>{t.description || 'Sin descripción'}</small></div>
                    <span className="chip">{t.category}</span>
                  </div>
                  <pre>{t.content}</pre>
                  <div className="routeros-template-foot">
                    <span className="muted small">{t.is_builtin ? 'Integrada' : 'Personalizada'}</span>
                    <div className="row-actions">
                      <Button size="sm" variant="ghost" onClick={() => { setTemplateForm({ name: t.name, description: t.description, category: t.category, content: t.content, variables: JSON.stringify(t.variables || [], null, 2) }); setPreviewContent(''); setPreviewOpen(true) }}><Play size={13} /> Previsualizar</Button>
                      <Button size="sm" variant="ghost" onClick={() => openTemplate(t)}><Pencil size={13} /> Editar</Button>
                      {!t.is_builtin && <Button size="sm" variant="ghost" onClick={() => void deleteTemplate(t)}><Trash2 size={13} /></Button>}
                    </div>
                  </div>
                </div>
              ))}
              {!templates.length && <EmptyState title="Sin plantillas" message="Crea una plantilla con variables para reutilizarla en despliegues revisados." icon={<FileCode2 size={24} />} />}
            </div>
          )}

          {tab === 'jobs' && (
            <div className="table-card">
              <div className="data-table routeros-jobs-table">
                <div className="data-row head"><span>Trabajo</span><span>Plantilla</span><span>Equipos</span><span>Creador</span><span>Estado</span><span /></div>
                {jobs.map(j => {
                  const st = jobStatus(j.status)
                  return (
                    <div className="data-row" key={j.id}>
                      <span className="cell-main"><b>{j.name}</b><small>{timeAgo(j.created_at)}</small></span>
                      <span>{j.template_name || 'Script directo'}</span>
                      <span className="mono">{j.success ?? 0}/{j.targets} OK{j.failed ? ` · ${j.failed} err` : ''}</span>
                      <span className="muted small">{j.created_by}{j.approved_by && <small>Aprobó: {j.approved_by}</small>}</span>
                      <span><StatusBadge status={st.status} text={st.label} /></span>
                      <span className="row-actions">
                        <Button size="sm" variant="ghost" onClick={() => void openJobDetail(j)}>Detalle</Button>
                        {j.status === 'pending_approval' && <Button size="sm" variant="secondary" onClick={() => void approveJob(j)} disabled={busy === 'approve-' + j.id}><ShieldCheck size={13} /> Aprobar</Button>}
                        {j.status === 'approved' && <Button size="sm" variant="primary" onClick={() => void runJob(j)} disabled={busy === 'run-' + j.id}><Play size={13} /> Ejecutar</Button>}
                      </span>
                    </div>
                  )
                })}
                {!jobs.length && <EmptyState title="Sin despliegues" message="Crea un trabajo, apruébalo y ejecútalo sobre los equipos seleccionados." icon={<Rocket size={24} />} />}
              </div>
            </div>
          )}

          {tab === 'credentials' && (
            <div className="table-card">
              <div className="warning-box"><ShieldCheck size={16} /><div><b>Credenciales cifradas y nunca visibles</b><span>Se guardan con AES-256-GCM. La contraseña solo se usa en el servidor al conectar por RouterOS API. Los trabajos requieren aprobación y quedan en auditoría.</span></div></div>
              <div className="data-table routeros-credentials-table">
                <div className="data-row head"><span>Nombre</span><span>Usuario</span><span>Puerto</span><span>Seguridad</span><span>Creada</span><span /></div>
                {credentials.map(c => (
                  <div className="data-row" key={c.id}>
                    <span className="cell-main"><b>{c.name}</b><small>{c.description || 'Sin descripción'}</small></span>
                    <span className="mono">{c.username}</span>
                    <span className="mono">{c.port}</span>
                    <span>{c.use_tls ? <span className="cred-pill">TLS{c.insecure_tls ? ' · sin verificar' : ''}</span> : <span className="muted small">API plana</span>}</span>
                    <span className="muted small">{c.created_at ? timeAgo(c.created_at) : '—'}</span>
                    <span className="row-actions">
                      <Button size="sm" variant="ghost" onClick={() => openCredential(c)}><Pencil size={13} /> Editar</Button>
                      <Button size="sm" variant="ghost" onClick={() => void deleteCredential(c)}><Trash2 size={13} /></Button>
                    </span>
                  </div>
                ))}
                {!credentials.length && <EmptyState title="Sin credenciales" message="Añade un usuario RouterOS API con permisos mínimos de lectura y escritura para gestionar equipos." icon={<KeyRound size={24} />} />}
              </div>
            </div>
          )}
        </>
      )}

      <Modal open={credentialOpen} onClose={() => setCredentialOpen(false)} title={editingCredential ? 'Editar credencial RouterOS' : 'Nueva credencial RouterOS'} subtitle="Se cifra en reposo y nunca se devuelve al navegador." width={620} footer={<><Button variant="ghost" onClick={() => setCredentialOpen(false)}>Cancelar</Button><Button variant="primary" onClick={() => void saveCredential()} disabled={busy === 'credential'}>{busy === 'credential' ? 'Guardando…' : 'Guardar'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={credentialForm.name} onChange={e => setCredentialForm({ ...credentialForm, name: e.target.value })} placeholder="RouterOS API · core" /></Field>
          <Field label="Usuario API"><input className="input mono" value={credentialForm.username} onChange={e => setCredentialForm({ ...credentialForm, username: e.target.value })} placeholder="netmonitor" /></Field>
          <Field label={editingCredential ? 'Contraseña (vacío = mantener)' : 'Contraseña'}><input className="input" type="password" value={credentialForm.password} onChange={e => setCredentialForm({ ...credentialForm, password: e.target.value })} /></Field>
          <Field label="Puerto API" hint="8728 API, 8729 API-SSL."><input className="input mono" type="number" value={credentialForm.port} onChange={e => setCredentialForm({ ...credentialForm, port: Number(e.target.value) })} /></Field>
        </div>
        <div className="check-row"><label><input type="checkbox" checked={credentialForm.use_tls} onChange={e => {
          const useTLS = e.target.checked
          setCredentialForm({
            ...credentialForm,
            use_tls: useTLS,
            port: credentialForm.port === 8728 && useTLS ? 8729 : credentialForm.port === 8729 && !useTLS ? 8728 : credentialForm.port,
          })
        }} /> Usar TLS (api-ssl)</label><label><input type="checkbox" checked={credentialForm.insecure_tls} onChange={e => setCredentialForm({ ...credentialForm, insecure_tls: e.target.checked })} /> Permitir certificado no válido</label></div>
        <Field label="Descripción"><textarea className="input" rows={2} value={credentialForm.description} onChange={e => setCredentialForm({ ...credentialForm, description: e.target.value })} /></Field>
        <div className="security-tip"><ShieldCheck size={15} /> Crea un usuario RouterOS dedicado con grupo limitado. NetMonitor no ejecuta shells ni comandos locales.</div>
      </Modal>

      <Modal open={assignOpen} onClose={() => setAssignOpen(false)} title="Asignar credencial RouterOS" subtitle={assignDevice?.name} width={600} footer={<><Button variant="ghost" onClick={() => setAssignOpen(false)}>Cerrar</Button><Button variant="primary" onClick={() => void saveAssign()} disabled={busy === 'assign'}>{busy === 'assign' ? 'Guardando…' : 'Aplicar'}</Button></>}>
        <Field label="Credencial"><select className="input" value={assignCredential} onChange={e => setAssignCredential(e.target.value)}><option value="">— Sin gestionar</option>{credentials.map(c => <option key={c.id} value={c.id}>{c.name} ({c.username}@{c.port}{c.use_tls ? ' TLS' : ''})</option>)}</select></Field>
        <Field label="Puerto API específico" hint="0 usa el puerto de la credencial."><input className="input mono" type="number" value={assignPort} onChange={e => setAssignPort(Number(e.target.value))} /></Field>
        {assignDevice && <div className="kv-inline"><span>Equipo</span><b>{assignDevice.name}</b><span>IP</span><b className="mono">{assignDevice.primary_ip}</b><span>Versión</span><b>{assignDevice.routeros_version || '—'}</b></div>}
        {testResult && <pre className="code-block">{JSON.stringify(testResult, null, 2)}</pre>}
      </Modal>

      <Modal open={templateOpen} onClose={() => setTemplateOpen(false)} title={editingTemplate ? 'Editar plantilla' : 'Nueva plantilla RouterOS'} subtitle="Plantilla Go con variables del equipo. Se revisa antes de desplegar." width={760} footer={<><Button variant="ghost" onClick={() => setTemplateOpen(false)}>Cancelar</Button><Button variant="secondary" onClick={() => { setPreviewContent(''); setPreviewOpen(true) }}><Play size={14} /> Previsualizar</Button><Button variant="primary" onClick={() => void saveTemplate()} disabled={busy === 'template'}>{busy === 'template' ? 'Guardando…' : 'Guardar'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={templateForm.name} onChange={e => setTemplateForm({ ...templateForm, name: e.target.value })} /></Field>
          <Field label="Categoría"><input className="input" value={templateForm.category} onChange={e => setTemplateForm({ ...templateForm, category: e.target.value })} placeholder="config, security, monitoring…" /></Field>
        </div>
        <Field label="Descripción"><input className="input" value={templateForm.description} onChange={e => setTemplateForm({ ...templateForm, description: e.target.value })} /></Field>
        <Field label="Script RouterOS" hint={'Variables: {{.Device.Name}}, {{.Device.IP}}, {{.Device.Tags}}, {{.Map.Name}}, {{index .Vars "zona"}}. Se ejecuta mediante /system/script, nunca por shell.'}><textarea className="input code-editor" rows={14} value={templateForm.content} onChange={e => setTemplateForm({ ...templateForm, content: e.target.value })} /></Field>
        <Field label="Variables de ejemplo (JSON)" hint="Opcional. Se inyectan en .Vars y en los trabajos."><textarea className="input code-editor" rows={3} value={templateForm.variables} onChange={e => setTemplateForm({ ...templateForm, variables: e.target.value })} /></Field>
        <div className="security-tip"><AlertTriangle size={15} /> Revisa el script y prueba primero. Un comando RouterOS mal escrito puede cortar el acceso al equipo.</div>
      </Modal>

      <Modal open={previewOpen} onClose={() => setPreviewOpen(false)} title="Previsualizar plantilla renderizada" width={760} footer={<><Button variant="ghost" onClick={() => setPreviewOpen(false)}>Cerrar</Button><Button variant="secondary" onClick={() => void preview()} disabled={busy === 'preview'}>{busy === 'preview' ? 'Generando…' : 'Renderizar'}</Button></>}>
        <Field label="Equipo de ejemplo"><select className="input" value={previewDevice} onChange={e => setPreviewDevice(e.target.value)}><option value="">Datos de ejemplo</option>{inventory.devices.map(d => <option key={d.id} value={d.id}>{d.name} · {d.primary_ip}</option>)}</select></Field>
        <pre className="code-block tall">{previewContent || 'Selecciona un equipo y pulsa Renderizar.'}</pre>
      </Modal>

      <Modal open={jobOpen} onClose={() => setJobOpen(false)} title="Nuevo despliegue RouterOS" subtitle="Primero se crea el plan; después se aprueba y se ejecuta." width={820} footer={<><Button variant="ghost" onClick={() => setJobOpen(false)}>Cancelar</Button><Button variant="primary" onClick={() => void createJob()} disabled={busy === 'job'}>{busy === 'job' ? 'Creando plan…' : 'Crear trabajo'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre del trabajo"><input className="input" value={jobForm.name} onChange={e => setJobForm({ ...jobForm, name: e.target.value })} placeholder="Configurar identidad y NTP" /></Field>
          <Field label="Plantilla"><select className="input" value={jobForm.template_id} onChange={e => selectTemplate(e.target.value)}><option value="">— Script directo</option>{templates.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}</select></Field>
        </div>
        {!jobForm.template_id && <Field label="Script directo"><textarea className="input code-editor" rows={8} value={jobForm.content} onChange={e => setJobForm({ ...jobForm, content: e.target.value })} /></Field>}
        <Field label="Variables (JSON)"><textarea className="input code-editor" rows={3} value={jobForm.variables} onChange={e => setJobForm({ ...jobForm, variables: e.target.value })} /></Field>
        <Field label={`Equipos objetivo (${jobForm.target_device_ids.length} seleccionados)`} hint="Solo aparecen equipos con credencial RouterOS asignada.">
          <div className="target-picker">
            {managedDevices.map(d => (
              <label key={d.id} className={jobForm.target_device_ids.includes(d.id) ? 'selected' : ''}>
                <input type="checkbox" checked={jobForm.target_device_ids.includes(d.id)} onChange={e => setJobForm({ ...jobForm, target_device_ids: e.target.checked ? [...jobForm.target_device_ids, d.id] : jobForm.target_device_ids.filter(x => x !== d.id) })} />
                <span><b>{d.name}</b><small className="mono">{d.primary_ip} · {d.routeros_version || 'sin versión'}</small></span>
              </label>
            ))}
            {!managedDevices.length && <span className="muted small">No hay equipos gestionados. Asigna credenciales en la pestaña Inventario.</span>}
          </div>
        </Field>
        <div className="security-tip"><ShieldCheck size={15} /> Todo despliegue requiere aprobación y queda en auditoría antes de ejecutarse.</div>
        <Field label="Notas"><textarea className="input" rows={2} value={jobForm.notes} onChange={e => setJobForm({ ...jobForm, notes: e.target.value })} /></Field>
        <div className="security-tip"><ShieldCheck size={15} /> El plan guarda el script renderizado y su hash por equipo. Las ejecuciones y aprobaciones quedan en auditoría.</div>
      </Modal>

      <Modal open={detailOpen} onClose={() => setDetailOpen(false)} title={jobDetail?.name || 'Trabajo'} subtitle={jobDetail ? `${jobDetail.targets} equipos · ${jobStatus(jobDetail.status).label}` : ''} width={900} footer={<>{jobDetail && <>{['pending_approval', 'approved'].includes(jobDetail.status) && <Button variant="ghost" onClick={() => void cancelJob(jobDetail)}>Cancelar</Button>}{['pending_approval', 'approved'].includes(jobDetail.status) && <Button variant="secondary" onClick={() => void runDry(jobDetail)} disabled={busy === 'dry-' + jobDetail.id}>Dry-run</Button>}{jobDetail.status === 'pending_approval' && <Button variant="primary" onClick={() => void approveJob(jobDetail)}><ShieldCheck size={14} /> Aprobar</Button>}{jobDetail.status === 'approved' && <Button variant="primary" onClick={() => void runJob(jobDetail)}><Play size={14} /> Ejecutar</Button>}{['completed', 'failed', 'cancelled'].includes(jobDetail.status) && <Button variant="danger" onClick={() => void deleteJob(jobDetail)}>Eliminar</Button>}</>}<Button variant="ghost" onClick={() => setDetailOpen(false)}>Cerrar</Button></>}>
        {jobDetail && <>
          <div className="kv-inline"><span>Creador</span><b>{jobDetail.created_by}</b><span>Aprobación</span><b>{jobDetail.approved_by || 'Pendiente'}</b><span>Inicio</span><b>{jobDetail.started_at ? timeAgo(jobDetail.started_at) : '—'}</b><span>Fin</span><b>{jobDetail.finished_at ? timeAgo(jobDetail.finished_at) : '—'}</b></div>
          {jobDetail.notes && <p className="muted small">{jobDetail.notes}</p>}
          <h4 className="section-mini-title">Plan por equipo</h4>
          <div className="data-table job-plan-table">
            {(jobDetail.plan || []).map(p => <div className="data-row" key={p.device_id}><span className="cell-main"><b>{p.device_name}</b><small className="mono">{p.primary_ip}</small></span><span className="mono small">{p.content_hash.slice(0, 16)}…</span><span className="muted small">{p.content.split('\n').length} líneas</span></div>)}
          </div>
          <h4 className="section-mini-title">Resultado por equipo</h4>
          <div className="data-table job-plan-table">
            {(jobDetail.target_rows || []).map(t => <div className="data-row" key={t.id}><span className="cell-main"><b>{t.device_name}</b><small>{t.output || t.error || 'Pendiente'}</small></span><span><StatusBadge status={t.status === 'success' ? 'up' : t.status === 'failed' ? 'down' : 'unknown'} text={t.status} /></span><span className="muted small">{t.finished_at ? timeAgo(t.finished_at) : '—'}</span></div>)}
          </div>
          {dryRun.length > 0 && <><h4 className="section-mini-title">Dry-run</h4><pre className="code-block">{JSON.stringify(dryRun, null, 2)}</pre></>}
        </>}
      </Modal>
    </div>
  )
}
