import { useEffect, useState } from 'react'
import { Check, Copy, Download, Globe, KeyRound, Monitor, Pencil, Terminal, Trash2 } from 'lucide-react'
import { apiDownload, apiGet, apiSend } from '../api'
import { confirmAction } from '../confirm'
import type { DeviceCredentialKind, DeviceToolAccess } from '../types'
import { Button, Field, Modal, Spinner } from '../ui'

const KINDS: DeviceCredentialKind[] = ['winbox', 'web', 'ssh']

const KIND_LABEL: Record<string, string> = {
  winbox: 'Winbox',
  web: 'Web / HTTP',
  ssh: 'SSH',
}

const KIND_ICON: Record<string, React.ReactNode> = {
  winbox: <Monitor size={15} />,
  web: <Globe size={15} />,
  ssh: <Terminal size={15} />,
}

function defaultPort(kind: string, useTLS = false): number {
  if (kind === 'winbox') return 8291
  if (kind === 'ssh') return 22
  if (kind === 'web') return useTLS ? 443 : 80
  return 0
}

function sourceLabel(source?: string): string {
  if (source === 'device') return 'credencial propia'
  if (source === 'routeros') return 'credencial RouterOS'
  return 'sin configurar'
}

function endpointHost(host: string): string {
  if (host.includes(':') && !host.startsWith('[')) return `[${host}]`
  return host
}

export default function DeviceAccessPanel({
  deviceId,
  deviceName,
  host,
}: {
  deviceId?: string
  deviceName?: string
  host?: string
}) {
  const [access, setAccess] = useState<DeviceToolAccess[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<DeviceCredentialKind | null>(null)
  const [saving, setSaving] = useState(false)
  const [busy, setBusy] = useState('')
  const [notice, setNotice] = useState('')
  const [bridgeError, setBridgeError] = useState('')
  const [bridgeKind, setBridgeKind] = useState<'' | 'winbox' | 'ssh'>('')
  const [bridgeUrl, setBridgeUrl] = useState('')
  const [form, setForm] = useState({
    username: '', password: '', port: 0, use_tls: false, path: '', notes: '',
  })

  const load = async () => {
    if (!deviceId) return
    setLoading(true)
    try {
      setAccess(await apiGet<DeviceToolAccess[]>(`/devices/${deviceId}/credentials`))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    setAccess([])
    setError('')
    setNotice('')
    setBridgeError('')
    setBridgeKind('')
    setBridgeUrl('')
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId])

  if (!deviceId) return null

  const entry = (kind: DeviceCredentialKind) => access.find(item => item.kind === kind)
  const winbox = entry('winbox')
  const web = entry('web')
  const ssh = entry('ssh')

  const openEditor = (kind: DeviceCredentialKind) => {
    const current = entry(kind)
    setEditing(kind)
    setError('')
    setForm({
      username: current?.username || '',
      password: '',
      port: current?.port || defaultPort(kind, current?.use_tls),
      use_tls: current?.use_tls || false,
      path: current?.path || '',
      notes: current?.notes || '',
    })
  }

  const save = async () => {
    if (!deviceId || !editing) return
    setSaving(true)
    setError('')
    try {
      await apiSend(`/devices/${deviceId}/credentials/${editing}`, 'PUT', {
        ...form,
        password: form.password || undefined,
      })
      setEditing(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const remove = async (kind: DeviceCredentialKind) => {
    if (!deviceId) return
    if (!await confirmAction(`¿Quitar la credencial ${KIND_LABEL[kind]} de este equipo?`, {
      title: 'Quitar credencial', confirmLabel: 'Quitar', danger: true,
    })) return
    try {
      await apiSend(`/devices/${deviceId}/credentials/${kind}`, 'DELETE')
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const reveal = async (kind: DeviceCredentialKind) => {
    if (!deviceId) return null
    return apiSend<{ username: string; password: string; source: string }>(
      `/devices/${deviceId}/credentials/${kind}/reveal`, 'POST',
    )
  }

  const copyText = async (value: string) => {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)
      return
    }
    const area = document.createElement('textarea')
    area.value = value
    area.style.position = 'fixed'
    area.style.opacity = '0'
    document.body.appendChild(area)
    area.select()
    document.execCommand('copy')
    area.remove()
  }

  const copyPassword = async (kind: DeviceCredentialKind) => {
    setError('')
    setNotice('')
    try {
      const data = await reveal(kind)
      if (!data?.password) {
        setNotice('La credencial no tiene contraseña guardada.')
        return
      }
      await copyText(data.password)
      setNotice(`Contraseña de ${KIND_LABEL[kind]} copiada.`)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const copyUser = async (kind: DeviceCredentialKind) => {
    const item = entry(kind)
    if (!item?.username) return
    await copyText(item.username)
    setNotice(`Usuario de ${KIND_LABEL[kind]} copiado.`)
  }

  const openBridge = async (kind: 'winbox' | 'ssh') => {
    if (!deviceId) return
    setBusy(kind)
    setBridgeError('')
    setBridgeKind('')
    setBridgeUrl('')
    try {
      const data = await apiSend<{ url: string }>('/tools/winbox/ticket', 'POST', { device_id: deviceId, kind })
      setBridgeUrl(data.url)
      window.location.href = data.url
      setBridgeKind(kind)
    } catch (e) {
      setBridgeError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const openWeb = async () => {
    if (!host) return
    const tls = !!web?.use_tls
    const port = web?.port || (tls ? 443 : 80)
    const path = web?.path || '/'
    const portPart = port === (tls ? 443 : 80) ? '' : `:${port}`
    window.open(`${tls ? 'https' : 'http'}://${endpointHost(host)}${portPart}${path}`, '_blank', 'noopener,noreferrer')
    if (web && web.source !== 'none') void copyPassword('web')
  }

  const downloadBridge = async () => {
    setBridgeError('')
    try {
      await apiDownload('/tools/winbox/bridge', 'netmonitor-bridge-installer.vbs')
    } catch (e) {
      setBridgeError((e as Error).message)
    }
  }

  const details = (kind: DeviceCredentialKind, item?: DeviceToolAccess) => {
    if (!item || item.source === 'none') return 'Sin credencial configurada'
    if (kind === 'winbox') return `Puerto ${item.port || 8291}`
    if (kind === 'ssh') return `Puerto ${item.port || 22}`
    return `${item.use_tls ? 'HTTPS' : 'HTTP'} · puerto ${item.port || (item.use_tls ? 443 : 80)}${item.path ? ` · ${item.path}` : ''}`
  }

  return (
    <div className="access-panel">
      <div className="access-toolbar">
        <Button size="sm" variant="primary" onClick={() => void openBridge('winbox')} disabled={!winbox || winbox.source === 'none' || busy === 'winbox'}>
          <Monitor size={14} /> {busy === 'winbox' ? 'Preparando…' : 'Winbox'}
        </Button>
        <Button size="sm" variant="secondary" onClick={() => void openBridge('ssh')} disabled={!ssh?.username || busy === 'ssh'}>
          <Terminal size={14} /> {busy === 'ssh' ? 'Preparando…' : 'SSH'}
        </Button>
        <Button size="sm" variant="secondary" onClick={() => void openWeb()} disabled={!host}>
          <Globe size={14} /> Web
        </Button>
      </div>

      <div className="access-note">
        Winbox y SSH se abren con el puente local. Web abre el navegador y copia la contraseña.
        El navegador no puede rellenar automáticamente el formulario de WebFig por seguridad.
      </div>
      <div>
        <Button size="sm" variant="ghost" onClick={() => void downloadBridge()}><Download size={13} /> Instalar o actualizar puente</Button>
      </div>

      {bridgeKind && (
        <div className="access-hint">
          Si no se abrió {bridgeKind === 'ssh' ? 'SSH' : 'Winbox'}, instala el puente y usa{' '}
          <a href={bridgeUrl} className="tool-link">abrir {bridgeKind === 'ssh' ? 'SSH' : 'Winbox'} ahora</a>.
          <Button size="sm" variant="secondary" onClick={() => void downloadBridge()}><Download size={13} /> Descargar puente</Button>
        </div>
      )}
      {bridgeError && <div className="form-error">{bridgeError}</div>}
      {error && <div className="form-error">{error}</div>}
      {notice && <div className="access-notice"><Check size={13} /> {notice}</div>}

      {loading ? <div className="access-loading"><Spinner /></div> : (
        <div className="access-list">
          {KINDS.map(kind => {
            const item = entry(kind)
            return (
              <div className="access-row" key={kind}>
                <div className={`access-icon ${item?.source === 'none' || !item ? 'neutral' : ''}`}>{KIND_ICON[kind]}</div>
                <div className="access-main">
                  <b>{KIND_LABEL[kind]}</b>
                  <span>{item?.username || 'Sin usuario'} · {sourceLabel(item?.source)}</span>
                  <small>{details(kind, item)}</small>
                </div>
                <div className="access-actions">
                  {item?.username && <Button size="sm" variant="ghost" onClick={() => void copyUser(kind)}><Copy size={12} /> Usuario</Button>}
                  {item?.has_password && <Button size="sm" variant="ghost" onClick={() => void copyPassword(kind)}><KeyRound size={12} /> Contraseña</Button>}
                  <Button size="sm" variant="secondary" onClick={() => openEditor(kind)}><Pencil size={12} /> Configurar</Button>
                  {item?.source === 'device' && <Button size="sm" variant="ghost" onClick={() => void remove(kind)}><Trash2 size={12} /></Button>}
                </div>
              </div>
            )
          })}
        </div>
      )}

      <div className="access-footnote">
        Si un servicio no tiene credencial propia, se reutiliza la credencial RouterOS asignada al equipo.
      </div>

      <Modal open={!!editing} onClose={() => setEditing(null)} title={`Credencial ${editing ? KIND_LABEL[editing] : ''}`} subtitle={deviceName || host} width={560}
        footer={<><Button variant="ghost" onClick={() => setEditing(null)}>Cancelar</Button><Button variant="primary" onClick={() => void save()} disabled={saving}>{saving ? 'Guardando…' : 'Guardar credencial'}</Button></>}>
        <div className="form-grid two">
          <Field label="Usuario">
            <input className="input" value={form.username} onChange={e => setForm({ ...form, username: e.target.value })} placeholder="admin" autoComplete="off" />
          </Field>
          <Field label="Contraseña" hint="Déjala vacía para mantener la actual o reutilizar la credencial RouterOS.">
            <input className="input" type="password" value={form.password} onChange={e => setForm({ ...form, password: e.target.value })} placeholder="••••••••" autoComplete="new-password" />
          </Field>
          <Field label="Puerto">
            <input className="input" type="number" min={0} max={65535} value={form.port} onChange={e => setForm({ ...form, port: Number(e.target.value) })} />
          </Field>
          {editing === 'web' && (
            <Field label="Ruta" hint="Por ejemplo /webfig/">
              <input className="input mono" value={form.path} onChange={e => setForm({ ...form, path: e.target.value })} placeholder="/" />
            </Field>
          )}
          {editing === 'web' && (
            <label className="check-label"><input type="checkbox" checked={form.use_tls} onChange={e => setForm({ ...form, use_tls: e.target.checked, port: e.target.checked ? 443 : 80 })} /> Usar HTTPS</label>
          )}
        </div>
        <Field label="Notas">
          <textarea className="input textarea" rows={2} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} placeholder="Usuario de solo lectura, VLAN de gestión…" />
        </Field>
      </Modal>
    </div>
  )
}
