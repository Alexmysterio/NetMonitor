import { useEffect, useState } from 'react'
import {
  Activity,
  Globe,
  Network,
  Play,
  Route,
  Server,
} from 'lucide-react'
import { apiSend } from '../api'
import { Button, Field, Modal, Spinner } from '../ui'

type ToolTab = 'ping' | 'traceroute' | 'dns' | 'tcp' | 'http'

const TABS: { id: ToolTab; label: string; icon: React.ReactNode }[] = [
  { id: 'ping', label: 'Ping', icon: <Activity size={14} /> },
  { id: 'traceroute', label: 'Traceroute', icon: <Route size={14} /> },
  { id: 'dns', label: 'DNS', icon: <Globe size={14} /> },
  { id: 'tcp', label: 'TCP', icon: <Server size={14} /> },
  { id: 'http', label: 'HTTP', icon: <Network size={14} /> },
]

export default function ToolsModal({
  open,
  onClose,
  host,
  name,
}: {
  open: boolean
  onClose: () => void
  host?: string
  name?: string
}) {
  const [tab, setTab] = useState<ToolTab>('ping')
  const [target, setTarget] = useState(host || '')
  const [port, setPort] = useState(443)
  const [url, setUrl] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<any>(null)

  useEffect(() => {
    if (!open) return
    setTarget(host || '')
    setUrl(host ? `http://${host}` : '')
    setResult(null)
    setError('')
  }, [open, host])

  const run = async () => {
    setError('')
    setResult(null)
    setLoading(true)
    try {
      if (tab === 'ping') setResult(await apiSend<any>('/tools/ping', 'POST', { host: target, count: 4 }))
      if (tab === 'traceroute') setResult(await apiSend<any>('/tools/traceroute', 'POST', { host: target, max_hops: 30 }))
      if (tab === 'dns') setResult(await apiSend<any>('/tools/dns', 'POST', { host: target }))
      if (tab === 'tcp') setResult(await apiSend<any>('/tools/tcp', 'POST', { host: target, port }))
      if (tab === 'http') setResult(await apiSend<any>('/tools/http', 'POST', { url }))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Diagnóstico de red" subtitle={name ? `${name} · ${host || ''}` : host} width={720}
      footer={<><Button variant="ghost" onClick={onClose}>Cerrar</Button><Button variant="primary" onClick={run} disabled={loading}>{loading ? <Spinner /> : <Play size={14} />} {loading ? 'Ejecutando…' : 'Ejecutar'}</Button></>}>
      <div className="segmented tool-tabs">
        {TABS.map(t => (
          <button key={t.id} className={tab === t.id ? 'active' : ''} onClick={() => { setTab(t.id); setResult(null); setError('') }}>
            {t.icon} {t.label}
          </button>
        ))}
      </div>

      <div className="form-grid two">
        {(tab === 'ping' || tab === 'traceroute' || tab === 'tcp') && (
          <Field label="Host / IP"><input className="input mono" value={target} onChange={e => setTarget(e.target.value)} placeholder="192.0.2.1" /></Field>
        )}
        {tab === 'dns' && (
          <Field label="Nombre o IP" hint="Consulta A/AAAA, CNAME, MX, NS, TXT y PTR."><input className="input mono" value={target} onChange={e => setTarget(e.target.value)} placeholder="equipo.red.local" /></Field>
        )}
        {tab === 'tcp' && (
          <Field label="Puerto"><input className="input" type="number" min={1} max={65535} value={port} onChange={e => setPort(Number(e.target.value))} /></Field>
        )}
        {tab === 'http' && (
          <Field label="URL" hint="Se usa HEAD con fallback a GET. Admite http:// y https://."><input className="input mono" value={url} onChange={e => setUrl(e.target.value)} placeholder="http://192.0.2.1" /></Field>
        )}
      </div>

      {error && <div className="form-error">{error}</div>}
      {loading && <div className="tool-loading"><Spinner label="Consultando…" /></div>}

      {result && tab === 'ping' && (
        <div className="tool-result">
          <div className="tool-metrics">
            <div><b>{result.received}/{result.sent}</b><span>respuestas</span></div>
            <div><b>{Number(result.loss_pct).toFixed(0)}%</b><span>pérdida</span></div>
            <div><b>{Number(result.min_ms).toFixed(1)}</b><span>mín. ms</span></div>
            <div><b>{Number(result.avg_ms).toFixed(1)}</b><span>media ms</span></div>
            <div><b>{Number(result.max_ms).toFixed(1)}</b><span>máx. ms</span></div>
            <div><b>{Number(result.stddev_ms).toFixed(1)}</b><span>jitter ms</span></div>
          </div>
        </div>
      )}

      {result && tab === 'traceroute' && (
        <div className="tool-result">
          <div className="walk-table compact">
            <div className="walk-row tool-trace-row head"><span>#</span><span>Salto</span><span>RTT</span></div>
            {(result.hops || []).map((h: any) => (
              <div className="walk-row tool-trace-row" key={h.hop}>
                <span className="mono">{h.hop}</span>
                <span className="mono">{h.ip}{h.final ? ' · destino' : ''}</span>
                <span className="mono">{h.rtt_ms == null ? '—' : `${Number(h.rtt_ms).toFixed(2)} ms`}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {result && tab === 'dns' && (
        <div className="tool-result">
          <div className="walk-table compact">
            <div className="walk-row tool-dns-row head"><span>Tipo</span><span>Valor</span></div>
            {(result.answers || []).map((a: any, i: number) => (
              <div className="walk-row tool-dns-row" key={`${a.type}-${i}`}><span>{a.type}</span><span className="mono">{a.value}</span></div>
            ))}
            {!result.answers?.length && <div className="walk-empty">Sin respuestas.</div>}
          </div>
        </div>
      )}

      {result && tab === 'tcp' && (
        <div className="tool-result">
          <div className={`tool-verdict ${result.open ? 'ok' : 'bad'}`}>
            <b>{result.open ? 'Puerto abierto' : 'Puerto cerrado o filtrado'}</b>
            <span>{result.host}:{result.port} · {Number(result.latency_ms).toFixed(2)} ms</span>
            {result.error && <span className="muted small">{result.error}</span>}
          </div>
        </div>
      )}

      {result && tab === 'http' && (
        <div className="tool-result">
          <div className={`tool-verdict ${result.ok ? 'ok' : 'bad'}`}>
            <b>{result.ok ? `${result.status_code} ${result.status || ''}` : (result.error || 'Sin respuesta HTTP')}</b>
            <span>{result.url}{result.latency_ms != null ? ` · ${Number(result.latency_ms).toFixed(0)} ms` : ''}</span>
          </div>
          <div className="tool-kv">
            {result.server && <div><span>Servidor</span><b>{result.server}</b></div>}
            {result.content_type && <div><span>Content-Type</span><b>{result.content_type}</b></div>}
            {result.tls_subject && <div><span>TLS</span><b>{result.tls_subject} · {result.tls_days_left} días</b></div>}
            {result.redirect && result.redirect !== result.url && <div><span>Destino final</span><b className="mono">{result.redirect}</b></div>}
          </div>
        </div>
      )}
    </Modal>
  )
}
