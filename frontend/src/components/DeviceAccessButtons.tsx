import { useEffect, useState } from 'react'
import { Check, Download, Globe, Monitor, Terminal } from 'lucide-react'
import { apiDownload, apiGet, apiSend } from '../api'
import type { DeviceCredentialKind, DeviceToolAccess } from '../types'
import { Button } from '../ui'

function endpointHost(host: string): string {
  if (host.includes(':') && !host.startsWith('[')) return `[${host}]`
  return host
}

export default function DeviceAccessButtons({
  deviceId,
  host,
  access,
}: {
  deviceId?: string
  host?: string
  access?: DeviceToolAccess[]
}) {
  const [internalAccess, setInternalAccess] = useState<DeviceToolAccess[]>([])
  const [loading, setLoading] = useState(access === undefined)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [bridgeKind, setBridgeKind] = useState<'' | 'winbox' | 'ssh'>('')
  const [bridgeUrl, setBridgeUrl] = useState('')

  useEffect(() => {
    setError('')
    setNotice('')
    setBridgeKind('')
    setBridgeUrl('')
    if (access !== undefined) {
      setLoading(false)
      return
    }
    if (!deviceId) {
      setLoading(false)
      return
    }
    let active = true
    setLoading(true)
    apiGet<DeviceToolAccess[]>(`/devices/${deviceId}/credentials`)
      .then(data => { if (active) setInternalAccess(data) })
      .catch((e: Error) => { if (active) setError(e.message) })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [deviceId, access])

  if (!deviceId) return null

  const items = access ?? internalAccess
  const entry = (kind: DeviceCredentialKind) => items.find(item => item.kind === kind)
  const winbox = entry('winbox')
  const ssh = entry('ssh')
  const web = entry('web')

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
    if (!deviceId) return
    setError('')
    setNotice('')
    try {
      const data = await apiSend<{ username: string; password: string; source: string }>(
        `/devices/${deviceId}/credentials/${kind}/reveal`, 'POST',
      )
      if (!data?.password) {
        setNotice('La credencial no tiene contraseña guardada.')
        return
      }
      await copyText(data.password)
      setNotice(`Contraseña de ${kind === 'ssh' ? 'SSH' : kind === 'web' ? 'Web' : 'Winbox'} copiada.`)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const openBridge = async (kind: 'winbox' | 'ssh') => {
    if (!deviceId) return
    setBusy(kind)
    setError('')
    setNotice('')
    setBridgeKind('')
    setBridgeUrl('')
    try {
      const data = await apiSend<{ url: string }>('/tools/winbox/ticket', 'POST', { device_id: deviceId, kind })
      const ticket = data.url.replace(/^netmonitor-winbox:\/\//, '')
      setBridgeUrl(data.url)
      const controller = new AbortController()
      const timeout = window.setTimeout(() => controller.abort(), 5000)
      try {
        const response = await fetch('http://127.0.0.1:47821/launch', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ server: window.location.origin, ticket }),
          signal: controller.signal,
        })
        window.clearTimeout(timeout)
        if (response.ok) {
          setNotice(kind === 'ssh' ? 'SSH lanzado.' : 'Winbox lanzado.')
          return
        }
        let message = 'El puente no pudo abrir la aplicación.'
        try {
          const body = await response.json()
          if (body?.error) message = String(body.error)
        } catch {
          /* keep generic message */
        }
        setError(message)
        return
      } catch {
        // The local helper is not running/installed. Never invoke the custom
        // protocol automatically: the browser would ask for permission.
        setBridgeKind(kind)
      } finally {
        window.clearTimeout(timeout)
      }
    } catch (e) {
      setError((e as Error).message)
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
    setError('')
    try {
      await apiDownload('/tools/winbox/bridge', 'netmonitor-bridge-installer.vbs')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="device-access-buttons">
      <div className="access-toolbar">
        <Button size="sm" variant="primary" onClick={() => void openBridge('winbox')} disabled={loading || !winbox || winbox.source === 'none' || busy === 'winbox'}>
          <Monitor size={14} /> {busy === 'winbox' ? 'Preparando…' : 'Winbox'}
        </Button>
        <Button size="sm" variant="secondary" onClick={() => void openBridge('ssh')} disabled={loading || !ssh?.username || busy === 'ssh'}>
          <Terminal size={14} /> {busy === 'ssh' ? 'Preparando…' : 'SSH'}
        </Button>
        <Button size="sm" variant="secondary" onClick={() => void openWeb()} disabled={loading || !host}>
          <Globe size={14} /> Web
        </Button>
      </div>
      {notice && <div className="access-notice"><Check size={13} /> {notice}</div>}
      {error && <div className="form-error">{error}</div>}
      {bridgeKind && (
        <div className="access-hint">
          El puente local no está activo. Instálalo para abrir {bridgeKind === 'ssh' ? 'SSH' : 'Winbox'} sin permisos del navegador.
          <a href={bridgeUrl} className="tool-link">Usar el protocolo (puede pedir permiso)</a>
          <Button size="sm" variant="secondary" onClick={() => void downloadBridge()}><Download size={13} /> Instalar puente</Button>
        </div>
      )}
    </div>
  )
}
