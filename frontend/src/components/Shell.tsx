import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom'
import {
  Activity,
  AlertTriangle,
  Bell,
  Boxes,
  Check,
  ChevronRight,
  CircleDot,
  Cpu,
  Database,
  FileText,
  LayoutDashboard,
  LineChart,
  ListChecks,
  LogOut,
  Map as MapIcon,
  Plus,
  Radar,
  Radio,
  Router as RouterIcon,
  Search,
  Settings,
  ShieldAlert,
  Siren,
  SlidersHorizontal,
  Upload,
  Volume2,
  VolumeX,
  Wifi,
  X,
} from 'lucide-react'
import { apiGet, apiSend, clearSession, getToken, getUser } from '../api'
import { useRealtime } from '../realtime'
import type { DashboardSummary, MapInfo } from '../types'
import { Button, Field, IconButton, Modal, STATUS_LABEL, StatusDot, statusClass } from '../ui'

export default function Shell({ children }: { children: React.ReactNode }) {
  const [maps, setMaps] = useState<MapInfo[]>([])
  const [summary, setSummary] = useState<DashboardSummary | null>(null)
  const [newMapOpen, setNewMapOpen] = useState(false)
  const [mapName, setMapName] = useState('')
  const [mapParent, setMapParent] = useState('')
  const [saving, setSaving] = useState(false)
  const [importError, setImportError] = useState('')
  const [importing, setImporting] = useState(false)
  const importRef = useRef<HTMLInputElement>(null)
  const [search, setSearch] = useState('')
  const navigate = useNavigate()
  const location = useLocation()
  const { connected, toasts, dismissToast, soundEnabled, setSoundEnabled, refreshKey } = useRealtime()
  const user = getUser()
  const activeOutages = summary?.active_outages ?? 0
  const activeOutagesUnack = summary?.active_outages_unacknowledged ?? activeOutages

  const load = async () => {
    try {
      const [m, s] = await Promise.all([
        apiGet<MapInfo[]>('/maps'),
        apiGet<DashboardSummary>('/dashboard/summary'),
      ])
      setMaps(m)
      setSummary(s)
    } catch {
      /* handled globally */
    }
  }

  useEffect(() => {
    if (!getToken()) return
    void load()
    const timer = window.setInterval(load, 20000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    if (refreshKey) void load()
  }, [refreshKey])

  useEffect(() => {
    const reload = () => {
      if (getToken()) void load()
    }
    window.addEventListener('netmonitor:maps-changed', reload)
    return () => window.removeEventListener('netmonitor:maps-changed', reload)
  }, [])

  const tree = useMemo(() => {
    const children = new Map<string, MapInfo[]>()
    const roots: MapInfo[] = []
    maps.forEach(m => {
      if (m.parent_map_id && maps.some(x => x.id === m.parent_map_id)) {
        const arr = children.get(m.parent_map_id) || []
        arr.push(m)
        children.set(m.parent_map_id, arr)
      } else {
        roots.push(m)
      }
    })
    return { roots, children }
  }, [maps])

  const handleCreateMap = async () => {
    if (!mapName.trim()) return
    setSaving(true)
    try {
      const data = await apiSend<{ id: string }>('/maps', 'POST', {
        name: mapName,
        parent_map_id: mapParent || null,
        description: '',
      })
      setNewMapOpen(false)
      setMapName('')
      setMapParent('')
      await load()
      navigate(`/maps/${data.id}`)
    } catch (e) {
      alert((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const handleImport = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    setImportError('')
    setImporting(true)
    try {
      const text = await file.text()
      const parsed = JSON.parse(text)
      if (!parsed || parsed.format !== 'netmonitor-map/v1') throw new Error('El archivo no es un mapa exportado por NetMonitor')
      const data = await apiSend<{ id: string }>('/maps/import', 'POST', {
        name: mapName.trim() || parsed.map?.name || file.name.replace(/\.json$/i, ''),
        parent_map_id: mapParent || null,
        data: parsed,
      })
      setNewMapOpen(false)
      setMapName('')
      setMapParent('')
      await load()
      navigate(`/maps/${data.id}`)
    } catch (e) {
      setImportError((e as Error).message)
    } finally {
      setImporting(false)
    }
  }

  const submitSearch = (e: React.FormEvent) => {
    e.preventDefault()
    if (search.trim()) navigate(`/devices?q=${encodeURIComponent(search.trim())}`)
  }

  const acknowledgeToast = async (toast: typeof toasts[number]) => {
    if (!toast.deviceId) return
    try {
      await apiSend(`/devices/${toast.deviceId}/acknowledge`, 'POST', { note: 'Ack desde NOC', duration_minutes: 0 })
      dismissToast(toast.id)
    } catch {
      /* handled globally */
    }
  }

  const acknowledgeAll = async () => {
    await Promise.all(toasts.filter(t => t.deviceId).map(t => acknowledgeToast(t)))
  }

  const isMapRoute = location.pathname.startsWith('/maps')
  const activeNav = (path: string) => location.pathname === path || (path !== '/maps' && location.pathname.startsWith(path))
  const pageTitle = () => {
    if (isMapRoute) return 'Mapa de red'
    switch (location.pathname) {
      case '/devices': return 'Inventario de dispositivos'
      case '/outages': return 'Caídas e incidencias'
      case '/probes': return 'Motor de monitorización'
      case '/networks': return 'Redes y listas'
      case '/panels': return 'Paneles NOC'
      case '/history': return 'Histórico global'
      case '/routeros': return 'Gestión RouterOS'
      case '/traps': return 'Traps SNMP'
      case '/reports': return 'Informes avanzados'
      case '/imports': return 'Importar desde The Dude'
      default: return 'Ajustes y avisos'
    }
  }

  const renderMapNode = (m: MapInfo, depth = 0): React.ReactNode => {
    const active = location.pathname === `/maps/${m.id}`
    const kids = tree.children.get(m.id) || []
    return (
      <div key={m.id}>
        <Link to={`/maps/${m.id}`} className={`map-tree-node ${active ? 'active' : ''}`} style={{ paddingLeft: 12 + depth * 14 }}>
          <span className={`map-tree-status ${statusClass(m.status)}`} />
          <span className="map-tree-name">{m.name}</span>
          <span className="map-tree-count">
            {m.counts.down > 0 ? <em className="danger">{m.counts.down}</em> : (m.counts.acknowledged || 0) > 0 ? <em className="ack">{m.counts.acknowledged}</em> : m.counts.warning > 0 ? <em className="warn">{m.counts.warning}</em> : null}
            {m.counts.up + m.counts.down + (m.counts.acknowledged || 0) + m.counts.warning + m.counts.unreachable + (m.counts.disabled || 0) + (m.counts.unknown || 0) > 0 && (
              <span>{m.counts.up + m.counts.down + (m.counts.acknowledged || 0) + m.counts.warning + m.counts.unreachable + (m.counts.disabled || 0) + (m.counts.unknown || 0)}</span>
            )}
          </span>
        </Link>
        {kids.map(k => renderMapNode(k, depth + 1))}
      </div>
    )
  }

  return (
    <div className={`app-shell ${location.pathname.startsWith('/maps/') ? 'has-map' : ''}`}>
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">
            <span className="brand-node n1" />
            <span className="brand-node n2" />
            <span className="brand-node n3" />
            <span className="brand-line l1" />
            <span className="brand-line l2" />
          </div>
          <div>
            <div className="brand-name">NetMonitor</div>
            <div className="brand-sub">Visual NOC</div>
          </div>
          <span className={`conn-dot ${connected ? 'on' : 'off'}`} title={connected ? 'Tiempo real conectado' : 'Reconectando…'} />
        </div>

        <nav className="side-nav">
          <div className="side-section-title">
            <span>Mapas</span>
            <button onClick={() => setNewMapOpen(true)} title="Nuevo mapa"><Plus size={14} /></button>
          </div>
          <div className="map-tree">
            {tree.roots.map(r => renderMapNode(r))}
            {maps.length === 0 && <div className="side-empty">Sin mapas todavía</div>}
          </div>
          <div className="side-section-title" style={{ marginTop: 18 }}><span>Operación</span></div>
          <Link to="/devices" className={`side-link ${activeNav('/devices') ? 'active' : ''}`}>
            <Boxes size={17} /><span>Dispositivos</span>
            <span className="side-count">{summary?.devices.total ?? '—'}</span>
          </Link>
          <Link to="/outages" className={`side-link ${activeNav('/outages') ? 'active' : ''}`}>
            <ShieldAlert size={17} /><span>Caídas e incidencias</span>
            {activeOutages > 0 && <span className={`side-count ${activeOutagesUnack > 0 ? 'urgent' : 'ack-count'}`}>{activeOutages}</span>}
          </Link>
          <Link to="/probes" className={`side-link ${activeNav('/probes') ? 'active' : ''}`}>
            <Activity size={17} /><span>Sondas y funciones</span>
          </Link>
          <Link to="/networks" className={`side-link ${activeNav('/networks') ? 'active' : ''}`}>
            <Radar size={17} /><span>Redes y listas</span>
          </Link>
          <Link to="/panels" className={`side-link ${activeNav('/panels') ? 'active' : ''}`}>
            <LayoutDashboard size={17} /><span>Paneles NOC</span>
          </Link>
          <Link to="/history" className={`side-link ${activeNav('/history') ? 'active' : ''}`}>
            <LineChart size={17} /><span>Histórico global</span>
          </Link>
          <div className="side-section-title" style={{ marginTop: 18 }}><span>Gestión</span></div>
          <Link to="/traps" className={`side-link ${activeNav('/traps') ? 'active' : ''}`}>
            <Siren size={17} /><span>Traps SNMP</span>
          </Link>
          <Link to="/reports" className={`side-link ${activeNav('/reports') ? 'active' : ''}`}>
            <FileText size={17} /><span>Informes avanzados</span>
          </Link>
          {user?.role === 'admin' && (
            <Link to="/imports" className={`side-link ${activeNav('/imports') ? 'active' : ''}`}>
              <Database size={17} /><span>Importar The Dude</span>
            </Link>
          )}
          {user?.role === 'admin' && (
            <Link to="/routeros" className={`side-link ${activeNav('/routeros') ? 'active' : ''}`}>
              <RouterIcon size={17} /><span>RouterOS</span>
            </Link>
          )}
          <Link to="/settings" className={`side-link ${activeNav('/settings') ? 'active' : ''}`}>
            <SlidersHorizontal size={17} /><span>Ajustes y avisos</span>
          </Link>
        </nav>

        <div className="sidebar-spacer" />

        <div className="side-status-card">
          <div className="side-status-head"><span>Estado de la red</span><span className="live-pill"><span />live</span></div>
          <div className="side-status-grid">
            <div><b className="ok">{summary?.devices.up ?? 0}</b><span>Operativos</span></div>
            <div><b className="warn">{summary?.devices.warning ?? 0}</b><span>Avisos</span></div>
            <div><b className="danger">{summary?.devices.down ?? 0}</b><span>Caídos</span></div>
            <div><b className="violet">{summary?.devices.unreachable ?? 0}</b><span>Aislados</span></div>
          </div>
          <div className="side-health-bar">
            <span className="hb-ok" style={{ flex: Math.max(summary?.devices.up ?? 1, 1) }} />
            <span className="hb-warn" style={{ flex: summary?.devices.warning ?? 0 }} />
            <span className="hb-down" style={{ flex: summary?.devices.down ?? 0 }} />
            <span className="hb-muted" style={{ flex: (summary?.devices.unreachable ?? 0) + (summary?.devices.disabled ?? 0) }} />
          </div>
        </div>

        <div className="side-user">
          <div className="avatar">{(user?.display_name || user?.username || 'A').slice(0, 1).toUpperCase()}</div>
          <div className="side-user-info">
            <b>{user?.display_name || user?.username || 'Admin'}</b>
            <span>{user?.role === 'admin' ? 'Administrador' : 'Operador'}</span>
          </div>
          <IconButton label="Cerrar sesión" onClick={() => { clearSession(); navigate('/login') }}><LogOut size={16} /></IconButton>
        </div>
      </aside>

      <div className="main">
        <header className="topbar">
          <div className="topbar-left">
            <div className="topbar-title">
              <LayoutDashboard size={17} />
              <span>{pageTitle()}</span>
            </div>
            {summary && (
              <div className="topbar-stats">
                <span className="tstat ok"><span />{summary.devices.up} operativos</span>
                <span className="tstat warn"><span />{summary.devices.warning} avisos</span>
                <span className="tstat danger"><span />{summary.devices.down} caídos</span>
                <span className="tstat muted"><span />{summary.devices.unreachable} aislados</span>
              </div>
            )}
          </div>
          <div className="topbar-actions">
            <form className="search-box" onSubmit={submitSearch}>
              <Search size={15} />
              <input value={search} onChange={e => setSearch(e.target.value)} placeholder="Buscar por IP, nombre, etiqueta…" />
              <kbd>↵</kbd>
            </form>
            <IconButton label={soundEnabled ? 'Silenciar alertas' : 'Activar sonido'} onClick={() => setSoundEnabled(!soundEnabled)} className={soundEnabled ? 'is-on' : ''}>
              {soundEnabled ? <Volume2 size={17} /> : <VolumeX size={17} />}
            </IconButton>
            <Link to="/outages" className="topbar-bell">
              <Bell size={18} />
              {activeOutages > 0 && <span className={activeOutagesUnack > 0 ? '' : 'is-ack'}>{activeOutages}</span>}
            </Link>
            <div className="topbar-avatar">{(user?.display_name || user?.username || 'A').slice(0, 1).toUpperCase()}</div>
          </div>
        </header>

        <div className="content">{children}</div>
      </div>

      <div className="toast-stack">
        {toasts.length > 1 && <button className="toast-ack-all" onClick={acknowledgeAll}><Check size={13} /> Reconocer visibles ({toasts.filter(t => t.deviceId).length})</button>}
        {toasts.map(t => (
          <div key={t.id} className={`toast toast-${t.severity}`}>
            <div className="toast-icon">{t.severity === 'critical' ? <ShieldAlert size={18} /> : <AlertTriangle size={18} />}</div>
            <div className="toast-body">
              <b>{t.title}</b>
              <span>{t.message}</span>
              <div className="toast-actions">
                {t.deviceId && <button onClick={() => acknowledgeToast(t)}><Check size={12} /> Ack</button>}
                {t.mapId && <button onClick={() => { navigate(`/maps/${t.mapId}`); dismissToast(t.id) }}>Abrir mapa</button>}
              </div>
            </div>
            <button onClick={() => dismissToast(t.id)}><X size={14} /></button>
          </div>
        ))}
      </div>

      <Modal
        open={newMapOpen}
        onClose={() => setNewMapOpen(false)}
        title="Nuevo mapa"
        subtitle="Crea un lienzo o un submapa jerárquico."
        footer={
          <>
            <Button variant="ghost" onClick={() => setNewMapOpen(false)}>Cancelar</Button>
            <Button variant="primary" onClick={handleCreateMap} disabled={saving || !mapName.trim()}>
              {saving ? 'Creando…' : 'Crear mapa'}
            </Button>
          </>
        }
      >
        <div className="form-grid">
          <Field label="Nombre del mapa">
            <input className="input" value={mapName} onChange={e => setMapName(e.target.value)} placeholder="Ej. Sede Norte · CPD" autoFocus />
          </Field>
          <Field label="Mapa padre (opcional)" hint="Si seleccionas un padre, este mapa aparecerá como submapa navegable.">
            <select className="input" value={mapParent} onChange={e => setMapParent(e.target.value)}>
              <option value="">— Ninguno (mapa raíz)</option>
              {maps.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
            </select>
          </Field>
        </div>

        <div className="import-box">
          <div className="import-box-icon"><Upload size={18} /></div>
          <div>
            <b>O importa un mapa exportado</b>
            <span>Archivo JSON de NetMonitor. Reutiliza los dispositivos existentes y conserva posiciones, formas y enlaces.</span>
          </div>
          <Button variant="secondary" size="sm" onClick={() => importRef.current?.click()} disabled={importing}>
            {importing ? 'Importando…' : 'Seleccionar JSON'}
          </Button>
          <input ref={importRef} type="file" accept="application/json,.json" hidden onChange={handleImport} />
        </div>
        {importError && <div className="form-error">{importError}</div>}
      </Modal>
    </div>
  )
}
