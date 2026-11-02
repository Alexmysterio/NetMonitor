import { useCallback, useEffect, useState } from 'react'
import { Radar, RefreshCcw } from 'lucide-react'
import { apiGet } from '../api'
import type { SnmpDeviceInfo } from '../types'
import { Button, Modal, Spinner } from '../ui'
import { formatBytes, ifStatus, ifTypeLabel, routeTypeLabel } from '../snmpFormat'

export default function SnmpInfoModal({
  open,
  onClose,
  deviceId,
  deviceName,
  deviceIP,
  onWalk,
}: {
  open: boolean
  onClose: () => void
  deviceId?: string
  deviceName?: string
  deviceIP?: string
  onWalk?: () => void
}) {
  const [info, setInfo] = useState<SnmpDeviceInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
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

  useEffect(() => {
    if (open && deviceId) void load()
  }, [open, deviceId, load])

  useEffect(() => {
    if (!open) {
      setInfo(null)
      setError('')
    }
  }, [open])

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Inventario SNMP"
      subtitle={deviceName ? `${deviceName} · ${deviceIP || ''}` : 'Lectura en vivo de MIB-II'}
      width={1040}
      footer={<>
        <Button variant="ghost" onClick={onClose}>Cerrar</Button>
        <Button variant="secondary" onClick={load} disabled={loading}><RefreshCcw size={14} /> Actualizar</Button>
        {onWalk && <Button variant="dark" onClick={onWalk}><Radar size={14} /> SNMP Walk</Button>}
      </>}
    >
      {loading && !info && <div className="snmp-loading"><Spinner label="Consultando sistema, IPs, interfaces, ARP y rutas…" /></div>}
      {error && <div className="form-error">{error}</div>}
      {info && (
        <div className="snmp-info">
          {info.warnings?.length > 0 && (
            <div className="snmp-warnings">
              {info.warnings.map((w, i) => <div key={i}>{w}</div>)}
            </div>
          )}

          <div className="snmp-hero">
            <div><span>Nombre</span><b>{info.system.name || '—'}</b></div>
            <div><span>Uptime</span><b>{info.system.uptime || '—'}</b></div>
            <div><span>ObjectID</span><b className="mono">{info.system.object_id || '—'}</b></div>
            <div><span>sysServices</span><b>{info.system.services || '—'}</b></div>
          </div>
          <div className="snmp-descr">{info.system.descr || 'Sin sysDescr'}</div>
          <div className="snmp-meta">
            <span><b>Contacto:</b> {info.system.contact || '—'}</span>
            <span><b>Ubicación:</b> {info.system.location || '—'}</span>
            <span><b>IPs:</b> {info.ips.length}</span>
            <span><b>Interfaces:</b> {info.interfaces.length}</span>
            <span><b>ARP:</b> {info.arp.length}</span>
            <span><b>Rutas:</b> {info.routes.length}</span>
          </div>

          <div className="snmp-section">
            <div className="snmp-section-title"><h3>Direcciones IP</h3><span>{info.ips.length}</span></div>
            {info.ips.length ? (
              <div className="snmp-table-wrap">
                <table className="snmp-table">
                  <thead><tr><th>Dirección</th><th>Máscara</th><th>Interfaz</th><th>Broadcast</th></tr></thead>
                  <tbody>
                    {info.ips.map(ip => (
                      <tr key={ip.address}>
                        <td className="mono">{ip.address}</td>
                        <td className="mono">{ip.mask || '—'}</td>
                        <td>{ip.if_name || `ifIndex ${ip.if_index}`} <span className="muted mono">({ip.if_index})</span></td>
                        <td>{ip.bcast === 1 ? 'sí' : ip.bcast === 0 ? 'no' : '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : <div className="snmp-empty">Sin direcciones IP.</div>}
          </div>

          <div className="snmp-section">
            <div className="snmp-section-title"><h3>Interfaces</h3><span>{info.interfaces.length}</span></div>
            {info.interfaces.length ? (
              <div className="snmp-table-wrap">
                <table className="snmp-table snmp-table-wide">
                  <thead><tr><th>#</th><th>Interfaz</th><th>MAC</th><th>Tipo</th><th>Admin</th><th>Oper</th><th>Velocidad</th><th>MTU</th><th>RX</th><th>TX</th><th>Errores / descartes</th></tr></thead>
                  <tbody>
                    {info.interfaces.map(iface => (
                      <tr key={iface.if_index}>
                        <td className="mono">{iface.if_index}</td>
                        <td>
                          <b>{iface.name}</b>
                          {(iface.alias || iface.descr) && <small>{iface.alias || iface.descr}</small>}
                        </td>
                        <td className="mono">{iface.mac || '—'}</td>
                        <td>{ifTypeLabel(iface.type)}</td>
                        <td><span className={iface.admin_status === 1 ? 'snmp-ok' : 'snmp-bad'}>{ifStatus(iface.admin_status)}</span></td>
                        <td><span className={iface.oper_status === 1 ? 'snmp-ok' : 'snmp-bad'}>{ifStatus(iface.oper_status)}</span></td>
                        <td>{iface.speed_mbps > 0 ? `${iface.speed_mbps.toLocaleString('es-ES')} Mbps` : '—'}</td>
                        <td className="mono">{iface.mtu || '—'}</td>
                        <td className="mono">{formatBytes(iface.in_octets)}</td>
                        <td className="mono">{formatBytes(iface.out_octets)}</td>
                        <td className="mono">
                          {iface.in_errors + iface.out_errors > 0 ? `${iface.in_errors}/${iface.out_errors}` : '0/0'}
                          <small>desc. {iface.in_discards}/{iface.out_discards}</small>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : <div className="snmp-empty">Sin interfaces.</div>}
          </div>

          <div className="snmp-section">
            <div className="snmp-section-title"><h3>Tabla ARP</h3><span>{info.arp.length}</span></div>
            {info.arp.length ? (
              <div className="snmp-table-wrap">
                <table className="snmp-table">
                  <thead><tr><th>IP</th><th>MAC</th><th>Interfaz</th></tr></thead>
                  <tbody>
                    {info.arp.map(row => (
                      <tr key={`${row.ip}-${row.mac}`}>
                        <td className="mono">{row.ip}</td>
                        <td className="mono">{row.mac}</td>
                        <td>{row.if_name || `ifIndex ${row.if_index}`}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : <div className="snmp-empty">Sin entradas ARP.</div>}
          </div>

          <div className="snmp-section">
            <div className="snmp-section-title"><h3>Rutas IPv4</h3><span>{info.routes.length}</span></div>
            {info.routes.length ? (
              <div className="snmp-table-wrap">
                <table className="snmp-table">
                  <thead><tr><th>Destino</th><th>Máscara</th><th>Gateway</th><th>Interfaz</th><th>Tipo</th><th>Métrica</th></tr></thead>
                  <tbody>
                    {info.routes.map((r, i) => (
                      <tr key={`${r.destination}-${r.mask}-${i}`}>
                        <td className="mono">{r.destination}</td>
                        <td className="mono">{r.mask}</td>
                        <td className="mono">{r.next_hop && r.next_hop !== '0.0.0.0' ? r.next_hop : '—'}</td>
                        <td>{r.if_name || (r.if_index ? `ifIndex ${r.if_index}` : '—')}</td>
                        <td>{routeTypeLabel(r.type)}</td>
                        <td className="mono">{r.metric}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : <div className="snmp-empty">Sin rutas IPv4.</div>}
          </div>
        </div>
      )}
    </Modal>
  )
}
