import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ArrowRight, Eye, EyeOff, LockKeyhole, Network, ShieldCheck, UserRound } from 'lucide-react'
import { api, setSession } from '../api'
import { Button } from '../ui'

export default function LoginPage() {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('admin')
  const [show, setShow] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const navigate = useNavigate()

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await api<{ success: boolean; token: string; user: any }>('/auth/login', {
        method: 'POST',
        body: JSON.stringify({ username, password }),
      })
      setSession(res.token, res.user)
      navigate('/maps')
    } catch (err) {
      setError((err as Error).message || 'No se pudo iniciar sesión')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-page">
      <div className="login-visual">
        <div className="login-visual-grid" />
        <div className="login-brand">
          <div className="brand-mark large">
            <span className="brand-node n1" />
            <span className="brand-node n2" />
            <span className="brand-node n3" />
            <span className="brand-line l1" />
            <span className="brand-line l2" />
          </div>
          <div>
            <div className="login-brand-name">NetMonitor</div>
            <div className="login-brand-sub">The visual network operations center</div>
          </div>
        </div>

        <div className="login-canvas">
          <div className="demo-zone zone-a" />
          <div className="demo-zone zone-b" />
          <div className="demo-edge edge-1" />
          <div className="demo-edge edge-2" />
          <div className="demo-edge edge-3" />
          <div className="demo-edge edge-4" />

          <div className="demo-node demo-core">
            <span className="demo-icon"><Network size={19} /></span>
            <b>CCR2004-Core</b>
            <span>10.20.0.1 · 1.4 ms</span>
            <em className="demo-badge ok">Operativo</em>
          </div>
          <div className="demo-node demo-sw">
            <span className="demo-icon"><ShieldCheck size={18} /></span>
            <b>CRS326-Distribución</b>
            <span>10.20.0.2 · 2.1 ms</span>
            <em className="demo-badge ok">Operativo</em>
          </div>
          <div className="demo-node demo-ap">
            <span className="demo-icon">📡</span>
            <b>AP-Norte</b>
            <span>10.20.4.22</span>
            <em className="demo-badge warn">Temperatura 68°</em>
          </div>
          <div className="demo-node demo-printer">
            <span className="demo-icon">🖨️</span>
            <b>IMP-Rack</b>
            <span>10.20.9.31</span>
            <em className="demo-badge down">Caído 9m</em>
          </div>
          <div className="demo-node demo-submap">
            <span>🗺️</span>
            <div><b>Sede Central</b><small>42 dispositivos</small></div>
            <em className="demo-pill">🟢 41 · 🟡 1</em>
          </div>
          <div className="demo-caption">
            <span className="live-pill"><span />live</span>
            Visibilidad total. Un vistazo. Cero ruido.
          </div>
        </div>

        <div className="login-footnote">Mapas jerárquicos · SNMP v3 · Alertas inteligentes · Docker Compose</div>
      </div>

      <div className="login-form-side">
        <div className="login-form-wrap">
          <div className="login-kicker">Network Operations Center</div>
          <h1>Bienvenido de nuevo</h1>
          <p className="login-subtitle">Accede a tu centro de operaciones para monitorizar la red en tiempo real.</p>

          <form onSubmit={submit} className="login-form">
            <label className="field">
              <span className="field-label">Usuario</span>
              <span className="input-with-icon">
                <UserRound size={16} />
                <input className="input" value={username} onChange={e => setUsername(e.target.value)} autoComplete="username" autoFocus />
              </span>
            </label>
            <label className="field">
              <span className="field-label">Contraseña</span>
              <span className="input-with-icon">
                <LockKeyhole size={16} />
                <input className="input" type={show ? 'text' : 'password'} value={password} onChange={e => setPassword(e.target.value)} autoComplete="current-password" />
                <button type="button" className="input-suffix" onClick={() => setShow(!show)} aria-label="Mostrar contraseña">
                  {show ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </span>
            </label>
            {error && <div className="login-error">{error}</div>}
            <Button variant="dark" className="login-submit" disabled={loading}>
              {loading ? 'Entrando…' : 'Entrar en NetMonitor'} <ArrowRight size={17} />
            </Button>
          </form>

          <div className="login-hint">
            <span>Acceso inicial</span> <b>admin</b> · contraseña definida en <code>ADMIN_PASSWORD</code> (<code>.env</code>).
          </div>
          <div className="login-legal">Self-hosted · Sin telemetría · Hecho para redes extensas</div>
        </div>
      </div>
    </div>
  )
}
