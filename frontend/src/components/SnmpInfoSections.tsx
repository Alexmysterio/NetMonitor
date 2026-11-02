import { useCallback, useEffect, useState } from 'react'
import { RefreshCcw } from 'lucide-react'
import { apiGet } from '../api'
import type { Device, SnmpDeviceInfo } from '../types'
import { Spinner } from '../ui'
import { formatBytes, ifStatus, routeTypeLabel } from '../snmpFormat'

type Section = 'system' | 'interfaces' | 'ips' | 'arp' | 'routes'

export default function SnmpInfoSections({ device }: { device: Device | null }) {
  const [info, setInfo] = useState<SnmpDeviceInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const deviceId = device?.id
  const refresh = device?.snmp_refresh
  const ifSec = refresh?.interfaces_sec ?? 60
  const ipSec = refresh?.ips_sec ?? 300
  const arpSec = refresh?.arp_sec ?? 300
  const routeSec = refresh?.routes_sec ?? 600
  const sysSec = refresh?.system_sec ?? 300

  const loadAll = useCallback(async () => {
    if (!deviceId) return
    setLoading(true)
    setError('')
    try {
      setInfo(await apiGet<SnmpDeviceInfo>(`/devices/${deviceId}/snmp-info`))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [deviceId])

  const loadSection = useCallback(async (section: Section) => {
    if (!deviceId) return
    try {
      const data = await apiGet<SnmpDeviceInfo>(`/devices/${deviceId}/snmp-info?section=${section}`)
      setInfo(prev => {
        if (!prev) return data
        return {
          ...prev,
          system: section === 'system' ? data.system : prev.system,
          interfaces: section === 'interfaces' ? data.interfaces : prev.interfaces,
          ips: section === 'ips' ? data.ips : prev.ips,
          arp: section === 'arp' ? data.arp : prev.arp,
          routes: section === 'routes' ? data.routes : prev.routes,
          warnings: data.warnings?.length ? data.warnings : prev.warnings,
        }
      })
    } catch {
      // Keep the last good snapshot; the manual refresh surfaces the error.
    }
  }, [deviceId])

  useEffect(() => {
    setInfo(null)
    setError('')
    if (deviceId) void loadAll()
  }, [deviceId, loadAll])

  const ready = !!info
  useEffect(() => {
    if (!ready || !deviceId || ifSec <= 0) return
    const t = window.setInterval(() => void loadSection('interfaces'), ifSec * 1000)
    return () => window.clearInterval(t)
  }, [ready, deviceId, ifSec, loadSection])
  useEffect(() => {
    if (!ready || !deviceId || ipSec <= 0) return
    const t = window.setInterval(() => void loadSection('ips'), ipSec * 1000)
    return () => window.clearInterval(t)
  }, [ready, deviceId, ipSec, loadSection])
  useEffect(() => {
    if (!ready || !deviceId || arpSec <= 0) return
    const t = window.setInterval(() => void loadSection('arp'), arpSec * 1000)
    return () => window.clearInterval(t)
  }, [ready, deviceId, arpSec, loadSection])
  useEffect(() => {
    if (!ready || !deviceId || routeSec <= 0) return
    const t = window.setInterval(() => void loadSection('routes'), routeSec * 1000)
    return () => window.clearInterval(t)
  }, [ready, deviceId, routeSec, loadSection])
  useEffect(() => {
    if (!ready || !deviceId || sysSec <= 0) return
    const t = window.setInterval(() => void loadSection('system'), sysSec * 1000)
    return () => window.clearInterval(t)
  }, [ready, deviceId, sysSec, loadSection])

  if (loading && !info) return <div className="snmp-loading"><Spinner label="Consultando SNMP…" /></div>
  if (error) return <div className="form-error">{error}</div>
  if (!info) return <div className="snmp-empty">Sin datos SNMP.</div>

  return (
    <div className="snmp-side">
      <div className="snmp-side-head">
        <div>
          <b>{info.system.name || 'Equipo SNMP'}</b>
          <span>{info.system.uptime || '—'} · {info.interfaces.length} interfaces</span>
        </div>
        <button onClick={loadAll} disabled={loading} title="Actualizar todo el inventario SNMP"><RefreshCcw size={13} /></button>
      </div>

      <details className="snmp-acc" open>
        <summary><span>Interfaces <em>{ifSec > 0 ? `auto ${ifSec}s` : 'manual'}</em></span><b>{info.interfaces.length}</b></summary>
        <div className="snmp-acc-body">
          {info.interfaces.map(iface => (
            <div className="snmp-side-row" key={iface.if_index}>
              <div className="snmp-side-row-head">
                <b title={iface.name}>{iface.name}</b>
                <span className={iface.oper_status === 1 ? 'snmp-ok' : 'snmp-bad'}>{ifStatus(iface.oper_status)}</span>
              </div>
              <div className="snmp-side-row-meta">
                if{iface.if_index} · {iface.mac || 'sin MAC'} · {iface.speed_mbps > 0 ? `${iface.speed_mbps.toLocaleString('es-ES')} Mbps` : '—'}
              </div>
              <div className="snmp-side-row-meta">
                RX {formatBytes(iface.in_octets)} · TX {formatBytes(iface.out_octets)} · err {iface.in_errors}/{iface.out_errors}
              </div>
              {iface.alias && <div className="snmp-side-row-alias">{iface.alias}</div>}
            </div>
          ))}
          {!info.interfaces.length && <div className="snmp-empty">Sin interfaces.</div>}
        </div>
      </details>

      <details className="snmp-acc">
        <summary><span>Direcciones IP <em>{ipSec > 0 ? `auto ${ipSec}s` : 'manual'}</em></span><b>{info.ips.length}</b></summary>
        <div className="snmp-acc-body">
          {info.ips.map(ip => (
            <div className="snmp-side-kv" key={ip.address}>
              <span className="mono">{ip.address}</span>
              <span>{ip.mask} · {ip.if_name || `if${ip.if_index}`}</span>
            </div>
          ))}
          {!info.ips.length && <div className="snmp-empty">Sin IPs.</div>}
        </div>
      </details>

      <details className="snmp-acc">
        <summary><span>Tabla ARP <em>{arpSec > 0 ? `auto ${arpSec}s` : 'manual'}</em></span><b>{info.arp.length}</b></summary>
        <div className="snmp-acc-body">
          {info.arp.map(row => (
            <div className="snmp-side-kv" key={`${row.ip}-${row.mac}`}>
              <span className="mono">{row.ip}</span>
              <span className="mono">{row.mac}</span>
            </div>
          ))}
          {!info.arp.length && <div className="snmp-empty">Sin ARP.</div>}
        </div>
      </details>

      <details className="snmp-acc">
        <summary><span>Rutas IPv4 <em>{routeSec > 0 ? `auto ${routeSec}s` : 'manual'}</em></span><b>{info.routes.length}</b></summary>
        <div className="snmp-acc-body">
          {info.routes.map((r, i) => (
            <div className="snmp-side-row" key={`${r.destination}-${r.mask}-${i}`}>
              <div className="snmp-side-row-head">
                <b className="mono">{r.destination}/{r.mask}</b>
                <span className="muted">{routeTypeLabel(r.type)}</span>
              </div>
              <div className="snmp-side-row-meta">
                {r.next_hop && r.next_hop !== '0.0.0.0' ? r.next_hop : 'directa'} · {r.if_name || `if${r.if_index}`} · metric {r.metric}
              </div>
            </div>
          ))}
          {!info.routes.length && <div className="snmp-empty">Sin rutas.</div>}
        </div>
      </details>

      {info.warnings?.length > 0 && (
        <div className="snmp-warnings">
          {info.warnings.map((w, i) => <div key={i}>{w}</div>)}
        </div>
      )}
    </div>
  )
}
