import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import {
  Background,
  BackgroundVariant,
  Connection,
  ConnectionMode,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Edge,
  type Node,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import dagre from '@dagrejs/dagre'
import { toPng, toSvg } from 'html-to-image'
import { jsPDF } from 'jspdf'
import {
  Activity,
  Cable,
  Check,
  ChevronRight,
  Crosshair,
  Download,
  FileImage,
  FileText,
  Fullscreen,
  Image as ImageIcon,
  Lock,
  Minimize2,
  Plus,
  Radar,
  Save,
  Settings,
  ShieldAlert,
  Trash2,
  Unlock,
  Upload,
  Wand2,
  Wifi,
} from 'lucide-react'
import { api, apiGet, apiSend, apiUpload, getUser } from '../api'
import { confirmAction } from '../confirm'
import { useRealtime } from '../realtime'
import type { Agent, CanvasData, CanvasElement, CanvasLink, Shape } from '../types'
import { Button, EmptyState, Field, IconButton, Modal, Spinner, StatusBadge, StatusDot, formatBps, statusClass } from '../ui'
import ShapeNode from '../components/ShapeNode'
import TrafficEdge, { TrafficLabelsContext } from '../components/TrafficEdge'
import Inspector from '../components/Inspector'
import AlertsDrawer from '../components/AlertsDrawer'
import AddNodeModal from '../components/AddNodeModal'
import DiscoveryModal from '../components/DiscoveryModal'
import LinkDiscoveryModal from '../components/LinkDiscoveryModal'
import { TextNodeMemo, ZoneNodeMemo } from '../components/MiscNode'
import { elementDisplaySize } from '../mapSizing'

const nodeTypes = { shapeNode: ShapeNode, zoneNode: ZoneNodeMemo, textNode: TextNodeMemo }
const edgeTypes = { traffic: TrafficEdge }

function elementToNode(el: CanvasElement, mapId?: string, onOpenSubmap?: (id: string) => void): Node {
  const type = el.element_type === 'zone' ? 'zoneNode' : el.element_type === 'text_label' ? 'textNode' : 'shapeNode'
  const size = elementDisplaySize(el)
  return {
    id: el.id,
    type,
    position: { x: Number(el.pos_x), y: Number(el.pos_y) },
    data: { element: el, mapId, onOpenSubmap },
    style: { width: size.width, height: size.height },
    zIndex: Number(el.z_index),
  }
}

function linkToEdge(link: CanvasLink): Edge {
  return {
    id: link.id,
    source: link.source_element_id,
    target: link.target_element_id,
    sourceHandle: link.source_handle || 'center',
    targetHandle: link.target_handle || 'center',
    type: 'traffic',
    data: { link },
    zIndex: 5,
  }
}

function MapView() {
  const { mapId } = useParams<{ mapId: string }>()
  const navigate = useNavigate()
  const { fitView, screenToFlowPosition, getViewport } = useReactFlow()
  const { lastMessage, subscribeMap, refreshKey } = useRealtime()
  const [data, setData] = useState<CanvasData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const zoneDragRef = useRef<{ id: string; x: number; y: number; children: Record<string, { x: number; y: number }> } | null>(null)
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [selectedNode, setSelectedNode] = useState<string | null>(null)
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null)
  const [design, setDesign] = useState(false)
  const canEdit = getUser()?.role !== 'viewer'
  const [showSnmpTraffic, setShowSnmpTraffic] = useState(false)
  const [mapSettingsOpen, setMapSettingsOpen] = useState(false)
  const [mapForm, setMapForm] = useState({
    name: '', description: '', grid_enabled: true, grid_size: 16, snap_to_grid: true,
    background_color: '#FFFFFF', default_device_label: '[Device.Name]\n[Device.FirstAddress]',
    default_device_shape: 'circle',
    background_image: '', background_opacity: 1,
    poll_interval_sec: 0, poll_down_retries: 0, poll_agent_id: '', poll_notifications: -1,
  })
  const [mapAgents, setMapAgents] = useState<Agent[]>([])
  const [backgroundUploading, setBackgroundUploading] = useState(false)
  const [exportOpen, setExportOpen] = useState(false)
  const [exporting, setExporting] = useState('')
  const canvasExportRef = useRef<HTMLDivElement>(null)
  const [dirty, setDirty] = useState(false)
  const [noc, setNoc] = useState(false)
  const [addOpen, setAddOpen] = useState(false)
  const [discoveryOpen, setDiscoveryOpen] = useState(false)
  const [linkDiscoveryOpen, setLinkDiscoveryOpen] = useState(false)
  const [addPos, setAddPos] = useState({ x: 100, y: 100 })
  const [saving, setSaving] = useState(false)
  const initialFit = useRef<string>('')
  const dirtyRef = useRef(false)
  const lastWsSeq = useRef(0)
  const loadSeq = useRef(0)
  dirtyRef.current = dirty

  const applyData = useCallback((next: CanvasData, fit: boolean) => {
    setData(next)
    setNodes((next.elements || []).map(el => elementToNode(el, mapId, id => navigate(`/maps/${id}`))))
    setEdges((next.links || []).map(linkToEdge))
    if (fit && initialFit.current !== next.map.id) {
      initialFit.current = next.map.id
      window.setTimeout(() => fitView({ padding: 0.18, maxZoom: 1.05, duration: 450 }), 80)
    }
  }, [fitView, navigate, setEdges, setNodes])

  const loadCanvas = useCallback(async (fit = false, silent = false) => {
    if (!mapId) return
    const requestID = ++loadSeq.current
    if (!silent) setLoading(true)
    try {
      const next = await apiGet<CanvasData>(`/maps/${mapId}/canvas`)
      if (requestID !== loadSeq.current) return
      applyData(next, fit)
      setError('')
      setDirty(false)
    } catch (e) {
      if (requestID === loadSeq.current) setError((e as Error).message)
    } finally {
      if (requestID === loadSeq.current) setLoading(false)
    }
  }, [mapId, applyData])

  useEffect(() => {
    if (!mapId) return
    initialFit.current = ''
    setSelectedNode(null)
    setSelectedEdge(null)
    setDirty(false)
    setError('')
    setData(null)
    setNodes([])
    setEdges([])
    void loadCanvas(true)
  }, [mapId, loadCanvas])

  useEffect(() => {
    if (mapId) subscribeMap(mapId)
  }, [mapId, subscribeMap])

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (!dirtyRef.current) void loadCanvas(false, true)
    }, 30000)
    return () => window.clearInterval(timer)
  }, [loadCanvas])

  useEffect(() => {
    if (refreshKey && !dirtyRef.current) void loadCanvas(false, true)
  }, [refreshKey, loadCanvas])

  useEffect(() => {
    if (noc) document.body.classList.add('noc-mode')
    else document.body.classList.remove('noc-mode')
    return () => document.body.classList.remove('noc-mode')
  }, [noc])

  useEffect(() => {
    if (!lastMessage || !data) return
    if (lastMessage.seq === lastWsSeq.current) return
    lastWsSeq.current = lastMessage.seq
    const msg = lastMessage
    if (msg.type === 'DEVICE_STATE_PATCH') {
      const p = msg.payload
      if (!p?.device_id) return
      setData(prev => {
        if (!prev) return prev
        return {
          ...prev,
          elements: prev.elements.map(el => {
            if (el.device_id !== p.device_id) return el
            const device = el.device ? {
              ...el.device,
              status: p.status,
              status_reason: p.message || el.device.status_reason,
              is_acknowledged: p.is_acknowledged,
              rtt_ms: p.rtt_ms ?? el.device.rtt_ms,
              packet_loss_pct: p.packet_loss_pct ?? el.device.packet_loss_pct,
              services_summary: p.services_summary || el.device.services_summary,
              root_cause_device_id: p.root_cause_device_id ?? el.device.root_cause_device_id,
              root_cause: p.root_cause ?? el.device.root_cause,
            } : null
            return { ...el, status: p.status, rendered_label: p.rendered_label || el.rendered_label, device }
          }),
        }
      })
      setNodes(prev => prev.map(node => {
        const el = (node.data as any).element as CanvasElement
        if (el.device_id !== p.device_id) return node
        const device = el.device ? {
          ...el.device,
          status: p.status,
          status_reason: p.message || el.device.status_reason,
          is_acknowledged: p.is_acknowledged,
          rtt_ms: p.rtt_ms ?? el.device.rtt_ms,
          services_summary: p.services_summary || el.device.services_summary,
          root_cause_device_id: p.root_cause_device_id ?? el.device.root_cause_device_id,
          root_cause: p.root_cause ?? el.device.root_cause,
        } : null
        const updated = { ...el, status: p.status, rendered_label: p.rendered_label || el.rendered_label, device }
        const size = elementDisplaySize(updated)
        return {
          ...node,
          style: { ...node.style, width: size.width, height: size.height },
          data: { ...node.data, element: updated },
        }
      }))
    }
    if (msg.type === 'SUBMAP_SUMMARY_PATCH') {
      const p = msg.payload
      if (!p?.submap_id) return
      setData(prev => prev ? {
        ...prev,
        elements: prev.elements.map(el => el.target_submap_id === p.submap_id ? {
          ...el, status: p.status, status_text: p.status_text,
          submap: el.submap ? { ...el.submap, status: p.status, counts: p.counts } : el.submap,
        } : el),
      } : prev)
      setNodes(prev => prev.map(node => {
        const el = (node.data as any).element as CanvasElement
        if (el.target_submap_id !== p.submap_id) return node
        const updated = {
          ...el, status: p.status, status_text: p.status_text,
          submap: el.submap ? { ...el.submap, status: p.status, counts: p.counts } : el.submap,
        }
        const size = elementDisplaySize(updated)
        return {
          ...node,
          style: { ...node.style, width: size.width, height: size.height },
          data: { ...node.data, element: updated },
        }
      }))
    }
    if (msg.type === 'MAP_SUMMARY_PATCH') {
      const p = msg.payload
      if (p?.map_id && p.map_id === mapId) {
        setData(prev => prev ? { ...prev, summary: { ...prev.summary, ...p, status: p.status } } : prev)
      }
    }
    if (msg.type === 'LINK_METRICS_PATCH' && Array.isArray(msg.payload)) {
      const updates = new Map<string, any>(msg.payload.map((x: any) => [x.link_id, x]))
      setData(prev => prev ? {
        ...prev,
        links: prev.links.map(l => {
          const u = updates.get(l.id)
          return u ? { ...l, status: u.status, tx_bps: u.tx_bps, rx_bps: u.rx_bps, utilization_pct: u.utilization_pct, rendered_label: u.rendered_label } : l
        }),
      } : prev)
      setEdges(prev => prev.map(edge => {
        const u = updates.get(edge.id)
        if (!u) return edge
        const link = (edge.data as any)?.link as CanvasLink
        return { ...edge, data: { ...edge.data, link: { ...link, status: u.status, tx_bps: u.tx_bps, rx_bps: u.rx_bps, utilization_pct: u.utilization_pct, utilization_level: u.status === 'down' ? 'down' : u.utilization_pct >= link.critical_utilization_pct ? 'critical' : u.utilization_pct >= link.warning_utilization_pct ? 'warning' : 'normal', rendered_label: u.rendered_label } } }
      }))
    }
  }, [lastMessage, data, mapId, setNodes, setEdges])

  const onConnect = async (connection: Connection) => {
    if (!mapId || !connection.source || !connection.target || connection.source === connection.target) return
    try {
      await apiSend(`/maps/${mapId}/links`, 'POST', {
        source_element_id: connection.source,
        target_element_id: connection.target,
        source_handle: 'center',
        target_handle: 'center',
        link_type: 'ethernet',
        max_speed_bps: 1_000_000_000,
      })
      await refreshAfterChange()
    } catch (e) {
      alert((e as Error).message)
    }
  }

  const saveLayout = async () => {
    if (!mapId) return
    setSaving(true)
    try {
      await apiSend(`/maps/${mapId}/layout`, 'PATCH', {
        nodes: nodes.map(n => ({
          id: n.id,
          pos_x: n.position.x,
          pos_y: n.position.y,
          width: Number((n.style as any)?.width) || 190,
          height: Number((n.style as any)?.height) || 96,
          rotation: Number((n.data as any)?.element?.rotation) || 0,
          zone_id: (n.data as any)?.element?.zone_id || '',
          z_index: n.zIndex ?? 10,
        })),
        viewport: getViewport(),
      })
      setDirty(false)
      return true
    } catch (e) {
      alert((e as Error).message)
      return false
    } finally {
      setSaving(false)
    }
  }

  const refreshAfterChange = async () => {
    if (dirtyRef.current) {
      const saved = await saveLayout()
      if (!saved) return
    }
    await loadCanvas(false, true)
  }

  const openMapSettings = () => {
    if (!data) return
    const m = data.map
    setMapForm({
      name: m.name,
      description: m.description || '',
      grid_enabled: m.grid_enabled,
      grid_size: m.grid_size || 16,
      snap_to_grid: m.snap_to_grid,
      background_color: m.background_color || '#FFFFFF',
      default_device_label: m.default_device_label || '[Device.Name]\n[Device.FirstAddress]',
      default_device_shape: m.default_device_shape || 'circle',
      background_image: m.background_image || '',
      background_opacity: m.background_opacity ?? 1,
      poll_interval_sec: m.poll_interval_sec || 0,
      poll_down_retries: m.poll_down_retries || 0,
      poll_agent_id: m.poll_agent_id || '',
      poll_notifications: m.poll_notifications ?? -1,
    })
    apiGet<Agent[]>('/agents').then(setMapAgents).catch(() => {})
    setMapSettingsOpen(true)
  }

  const saveMapSettings = async () => {
    if (!mapId) return
    setSaving(true)
    try {
      await apiSend(`/maps/${mapId}`, 'PATCH', {
        ...mapForm,
        default_device_shape: mapForm.default_device_shape,
      })
      window.dispatchEvent(new Event('netmonitor:maps-changed'))
      setMapSettingsOpen(false)
      await refreshAfterChange()
    } catch (e) {
      alert((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const deleteMap = async () => {
    if (!mapId || !data) return
    if (!await confirmAction(`¿Eliminar el mapa "${data.map.name}"? Se eliminarán sus elementos y enlaces.`, {
      title: 'Eliminar mapa', confirmLabel: 'Eliminar', danger: true,
    })) return
    setSaving(true)
    try {
      await apiSend(`/maps/${mapId}`, 'DELETE')
      window.dispatchEvent(new Event('netmonitor:maps-changed'))
      const parent = data.map.parent_map_id
      navigate(parent ? `/maps/${parent}` : '/maps')
    } catch (e) {
      alert((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const uploadBackground = async (file: File | null) => {
    if (!mapId || !file) return
    setBackgroundUploading(true)
    try {
      const form = new FormData()
      form.append('file', file)
      const data = await apiUpload<{ background_image: string }>(`/maps/${mapId}/background`, form)
      setMapForm(prev => ({ ...prev, background_image: data.background_image }))
    } catch (e) {
      alert((e as Error).message)
    } finally {
      setBackgroundUploading(false)
    }
  }

  const autoLayout = () => {
    const isZone = (n: Node) => ((n.data as any)?.element?.element_type === 'zone')
    const isText = (n: Node) => ((n.data as any)?.element?.element_type === 'text_label')
    const isBack = (n: Node) => ((n.data as any)?.element?.custom_style?.system === 'back')
    const layoutable = nodes.filter(n => !isZone(n) && !isText(n) && !isBack(n))
    if (!layoutable.length) return
    const g = new dagre.graphlib.Graph({ multigraph: true })
    g.setGraph({ rankdir: 'LR', nodesep: 70, ranksep: 120, marginx: 60, marginy: 60 })
    g.setDefaultEdgeLabel(() => ({}))
    for (const n of layoutable) {
      g.setNode(n.id, {
        width: Number((n.style as any)?.width) || 190,
        height: Number((n.style as any)?.height) || 96,
      })
    }
    const ids = new Set(layoutable.map(n => n.id))
    for (const e of edges) {
      if (ids.has(e.source) && ids.has(e.target)) g.setEdge(e.source, e.target, {}, e.id)
    }
    dagre.layout(g)
    const positions = new Map<string, { x: number; y: number }>()
    for (const n of layoutable) {
      const p = g.node(n.id)
      if (!p) continue
      positions.set(n.id, {
        x: Math.round(p.x - (Number((n.style as any)?.width) || 190) / 2),
        y: Math.round(p.y - (Number((n.style as any)?.height) || 96) / 2),
      })
    }
    setNodes(prev => prev.map(n => {
      const p = positions.get(n.id)
      return p ? { ...n, position: p } : n
    }))
    setDirty(true)
    window.setTimeout(() => fitView({ padding: 0.2, maxZoom: 1.05, duration: 450 }), 120)
  }

  const exportFilename = (ext: string) => {
    const base = (data?.map.name || 'mapa').toLowerCase().normalize('NFD').replace(/[\u0300-\u036f]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'mapa'
    return `${base}-${new Date().toISOString().slice(0, 10)}.${ext}`
  }

  const downloadBlob = (href: string, name: string) => {
    const a = document.createElement('a')
    a.href = href
    a.download = name
    a.click()
  }

  const exportImage = async (format: 'png' | 'svg' | 'pdf') => {
    if (!canvasExportRef.current || !data) return
    setExporting(format)
    try {
      fitView({ padding: 0.12, maxZoom: 1.2, duration: 0 })
      await new Promise(resolve => window.setTimeout(resolve, 350))
      const el = canvasExportRef.current
      const filter = (node: HTMLElement) => {
        const cls = node?.classList
        if (!cls) return true
        return !cls.contains('react-flow__controls') && !cls.contains('react-flow__minimap') && !cls.contains('react-flow__panel')
      }
      const options = {
        backgroundColor: data.map.background_color || '#ffffff',
        width: el.clientWidth,
        height: el.clientHeight,
        pixelRatio: 2,
        filter,
        cacheBust: true,
      }
      if (format === 'svg') {
        const svg = await toSvg(el, options)
        downloadBlob(svg, exportFilename('svg'))
      } else {
        const png = await toPng(el, options)
        if (format === 'png') {
          downloadBlob(png, exportFilename('png'))
        } else {
          const img = new Image()
          img.src = png
          await new Promise<void>((resolve, reject) => { img.onload = () => resolve(); img.onerror = () => reject(new Error('No se pudo preparar la imagen')) })
          const landscape = img.width >= img.height
          const pdf = new jsPDF({ orientation: landscape ? 'landscape' : 'portrait', unit: 'px', format: [img.width, img.height], compress: true })
          pdf.addImage(png, 'PNG', 0, 0, img.width, img.height)
          pdf.save(exportFilename('pdf'))
        }
      }
      setExportOpen(false)
    } catch (e) {
      alert(`No se pudo exportar: ${(e as Error).message}`)
    } finally {
      setExporting('')
    }
  }

  const handleNodesChange = useCallback((changes: any[]) => {
    onNodesChange(changes)
    if (changes.some(c => c.type === 'position' && c.dragging)) setDirty(true)
  }, [onNodesChange])

  const snapToZones = useCallback((node: Node) => {
    const threshold = 14
    let x = node.position.x
    let y = node.position.y
    const w = Number((node.style as any)?.width) || 190
    const h = Number((node.style as any)?.height) || 96
    for (const zone of nodes) {
      if (zone.id === node.id || (zone.data as any)?.element?.element_type !== 'zone') continue
      const zx = zone.position.x
      const zy = zone.position.y
      const zw = Number((zone.style as any)?.width) || 320
      const zh = Number((zone.style as any)?.height) || 220
      if (Math.abs(x - zx) < threshold) x = zx
      if (Math.abs(x + w - (zx + zw)) < threshold) x = zx + zw - w
      if (Math.abs(y - zy) < threshold) y = zy
      if (Math.abs(y + h - (zy + zh)) < threshold) y = zy + zh - h
    }
    return { x, y }
  }, [nodes])

  const exportJSON = async () => {
    if (!mapId || !data) return
    const payload = await api<any>(`/maps/${mapId}/export`)
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = exportFilename('json')
    a.click()
    URL.revokeObjectURL(url)
    setExportOpen(false)
  }

  const openAdd = () => {
    const pos = screenToFlowPosition({ x: window.innerWidth * 0.52, y: window.innerHeight * 0.45 })
    setAddPos({ x: Math.round(pos.x - 90), y: Math.round(pos.y - 48) })
    setAddOpen(true)
  }

  const selectNode = (_: React.MouseEvent, node: Node) => {
    setSelectedNode(node.id)
    setSelectedEdge(null)
  }
  const selectEdge = (_: React.MouseEvent, edge: Edge) => {
    setSelectedEdge(edge.id)
    setSelectedNode(null)
  }
  const closeInspector = () => { setSelectedNode(null); setSelectedEdge(null) }

  const doubleClickNode = (_: React.MouseEvent, node: Node) => {
    const el = (node.data as any).element as CanvasElement
    if (el.element_type === 'submap' && el.target_submap_id) navigate(`/maps/${el.target_submap_id}`)
  }

  const summary = data?.summary
  const breadcrumbs = data?.breadcrumbs || []

  if (loading && !data) {
    return <div className="page-center"><Spinner label="Cargando lienzo" /></div>
  }
  if (error && !data) {
    return <div className="page-center"><EmptyState title="No se pudo cargar el mapa" message={error} icon={<ShieldAlert size={24} />} /></div>
  }
  if (!data) return null

  return (
    <div className={`map-page ${noc ? 'noc' : ''}`}>
      <div className="map-main">
        <div className="map-header">
          <div className="map-title-wrap">
            <div className="breadcrumbs">
              {breadcrumbs.map((b, i) => (
                <span key={b.id} className="crumb">
                  {i > 0 && <ChevronRight size={12} />}
                  <button onClick={() => navigate(`/maps/${b.id}`)} className={i === breadcrumbs.length - 1 ? 'current' : ''}>{b.name}</button>
                </span>
              ))}
            </div>
            <div className="map-title-row">
              <h1>{data.map.name}</h1>
              <div className="map-summary-inline">
                <span className="ok"><i />{summary?.up ?? 0}</span>
                <span className="warn"><i />{summary?.warning ?? 0}</span>
                <span className="danger"><i />{summary?.down ?? 0}</span>
                <span className="ack"><i />{summary?.acknowledged ?? 0}</span>
                <span className="violet"><i />{summary?.unreachable ?? 0}</span>
              </div>
            </div>
            <p>{data.map.description || 'Sin descripción. Usa el modo edición para organizar los elementos.'}</p>
          </div>
          <div className="map-actions">
            {canEdit && <Button variant={design ? 'dark' : 'secondary'} size="sm" onClick={() => setDesign(!design)}>
              {design ? <Unlock size={14} /> : <Lock size={14} />} {design ? 'Edición activa' : 'Editar mapa'}
            </Button>}
            {design && (
              <>
                <Button variant="primary" size="sm" onClick={openAdd}><Plus size={15} /> Añadir</Button>
                <IconButton label="Descubrir equipos en la red" onClick={() => setDiscoveryOpen(true)}><Radar size={16} /></IconButton>
                <IconButton label="Descubrir enlaces L2 (LLDP/CDP/MNDP)" onClick={() => setLinkDiscoveryOpen(true)}><Cable size={16} /></IconButton>
                <Button variant="secondary" size="sm" onClick={autoLayout} title="Organizar automáticamente según los enlaces">
                  <Wand2 size={14} /> Organizar
                </Button>
                <Button variant="secondary" size="sm" onClick={saveLayout} disabled={!dirty || saving} className={dirty ? 'dirty-btn' : ''}>
                  <Save size={14} /> {saving ? 'Guardando…' : dirty ? 'Guardar' : 'Guardado'}
                </Button>
              </>
            )}
            <IconButton label={showSnmpTraffic ? 'Ocultar tráfico SNMP' : 'Mostrar tráfico SNMP'} onClick={() => setShowSnmpTraffic(!showSnmpTraffic)} className={showSnmpTraffic ? 'is-on' : ''}><Activity size={16} /></IconButton>
            <IconButton label="Ajustes del mapa" onClick={openMapSettings}><Settings size={16} /></IconButton>
            <IconButton label="Centrar mapa" onClick={() => fitView({ padding: 0.18, maxZoom: 1.05, duration: 400 })}><Crosshair size={16} /></IconButton>
            <IconButton label="Exportar mapa" onClick={() => setExportOpen(true)}><Download size={16} /></IconButton>
            <IconButton label={noc ? 'Salir de Modo NOC' : 'Modo NOC'} onClick={() => setNoc(!noc)} className={noc ? 'is-on' : ''}>
              {noc ? <Minimize2 size={16} /> : <Fullscreen size={16} />}
            </IconButton>
          </div>
        </div>

        <div className="canvas-shell" style={{ background: data.map.background_color || '#fff' }}>
          <div className="canvas-export-area" ref={canvasExportRef} style={{ background: data.map.background_color || '#fff' }}>
          {data.map.background_image && (
            <div
              className="map-background-image"
              style={{ backgroundImage: `url(${data.map.background_image})`, opacity: Number(data.map.background_opacity ?? 1) }}
            />
          )}
          <TrafficLabelsContext.Provider value={showSnmpTraffic}>
          <ReactFlow
            nodes={nodes}
            edges={edges}
            connectionMode={ConnectionMode.Loose}
            onNodesChange={handleNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeClick={selectNode}
            onNodeDragStart={(_, node) => {
              const el = (node.data as any)?.element
              if (el?.element_type === 'zone') {
                const children: Record<string, { x: number; y: number }> = {}
                for (const n of nodes) {
                  if ((n.data as any)?.element?.zone_id === node.id) children[n.id] = { ...n.position }
                }
                zoneDragRef.current = { id: node.id, x: node.position.x, y: node.position.y, children }
              }
            }}
            onNodeDragStop={(_, node) => {
              if (!design) return
              const pos = snapToZones(node)
              const el = (node.data as any)?.element
              const drag = zoneDragRef.current
              zoneDragRef.current = null
              setNodes(ns => ns.map(n => {
                if (n.id === node.id) {
                  let zoneID = (n.data as any)?.element?.zone_id || ''
                  if (el?.element_type !== 'zone') {
                    const w = Number((n.style as any)?.width) || 180
                    const h = Number((n.style as any)?.height) || 90
                    const cx = pos.x + w / 2
                    const cy = pos.y + h / 2
                    const zone = ns.find(z => (z.data as any)?.element?.element_type === 'zone'
                      && cx >= z.position.x && cx <= z.position.x + (Number((z.style as any)?.width) || 320)
                      && cy >= z.position.y && cy <= z.position.y + (Number((z.style as any)?.height) || 220))
                    zoneID = zone?.id || ''
                  }
                  return { ...n, position: pos, data: { ...(n.data as any), element: { ...(n.data as any).element, zone_id: zoneID } } }
                }
                if (drag?.id === node.id) {
                  const start = drag.children[n.id]
                  if (start) {
                    return {
                      ...n,
                      position: { x: start.x + pos.x - drag.x, y: start.y + pos.y - drag.y },
                    }
                  }
                }
                return n
              }))
              setDirty(true)
            }}
            onEdgeClick={selectEdge}
            onNodeDoubleClick={doubleClickNode}
            onPaneClick={closeInspector}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            nodesDraggable={design}
            nodesConnectable={design}
            elevateNodesOnSelect={false}
            elementsSelectable
            snapToGrid={data.map.snap_to_grid}
            snapGrid={[data.map.grid_size || 16, data.map.grid_size || 16]}
            zoomOnDoubleClick={false}
            deleteKeyCode={null}
            minZoom={0.15}
            maxZoom={2.5}
            proOptions={{ hideAttribution: true }}
            className="map-flow"
          >
            {data.map.grid_enabled && <Background variant={BackgroundVariant.Dots} gap={data.map.grid_size || 18} size={1.2} color="#dbe3ee" />}
            <Controls showInteractive={false} position="bottom-left" />
            <MiniMap
              position="bottom-right"
              pannable
              zoomable
              nodeColor={(n: any) => {
                const st = n?.data?.element?.status
                return st === 'down' ? '#dc2626' : st === 'warning' ? '#f59e0b' : st === 'unreachable' ? '#ea580c' : st === 'up' ? '#16a34a' : '#cbd5e1'
              }}
              maskColor="rgba(255,255,255,0.6)"
              nodeStrokeWidth={0}
              style={{ width: 130, height: 92 }}
            />
          </ReactFlow>
          </TrafficLabelsContext.Provider>
          </div>

          {data.elements?.length === 0 && (
            <div className="canvas-empty">
              <EmptyState icon={<Wifi size={26} />} title="Lienzo vacío" message="Activa el modo edición y añade tu primer dispositivo o submapa." />
              {design && <Button variant="primary" onClick={openAdd}><Plus size={15} /> Añadir primer nodo</Button>}
            </div>
          )}

          <div className="canvas-legend">
            <span><i className="lg-up" />Operativo</span>
            <span><i className="lg-warn" />Aviso</span>
            <span><i className="lg-down" />Caído</span>
            <span><i className="lg-unreach" />Aislado</span>
            <span><i className="lg-ack" />Ack</span>
          </div>

          {dirty && <div className="unsaved-pill"><span /> Cambios sin guardar</div>}

          <AlertsDrawer currentMapId={mapId!} onNavigate={id => navigate(`/maps/${id}`)} onRefresh={refreshAfterChange} />

          {selectedNode && <div className="inspector-backdrop" onClick={closeInspector} />}
          {selectedEdge && <div className="inspector-backdrop" onClick={closeInspector} />}
        </div>
      </div>

      {!noc && (
        <Inspector
          data={data}
          mapId={mapId!}
          selectedNodeId={selectedNode}
          selectedEdgeId={selectedEdge}
          onClose={closeInspector}
          onRefresh={refreshAfterChange}
          onNavigate={id => navigate(`/maps/${id}`)}
          refreshKey={refreshKey}
        />
      )}

      {noc && (
        <div className="noc-banner">
          <div className="noc-brand"><span className="brand-node n1" /><span className="brand-line l1" /><span className="brand-node n2" /> NetMonitor</div>
          <div className="noc-stats">
            <span className="ok">{summary?.up ?? 0} up</span>
            <span className="warn">{summary?.warning ?? 0} warn</span>
            <span className="danger">{summary?.down ?? 0} down</span>
          </div>
          <Button variant="ghost" size="sm" onClick={() => setNoc(false)}><Minimize2 size={14} /> Salir</Button>
        </div>
      )}

      <Modal open={mapSettingsOpen} onClose={() => setMapSettingsOpen(false)} title="Ajustes del mapa" subtitle={data?.map?.name} width={680}
        footer={<>
          {canEdit && <Button variant="danger" style={{ marginRight: 'auto' }} onClick={() => void deleteMap()} disabled={saving}><Trash2 size={14} /> Eliminar mapa</Button>}
          <Button variant="ghost" onClick={() => setMapSettingsOpen(false)}>Cancelar</Button>
          <Button variant="primary" onClick={saveMapSettings} disabled={saving}>{saving ? 'Guardando…' : 'Guardar ajustes'}</Button>
        </>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={mapForm.name} onChange={e => setMapForm({ ...mapForm, name: e.target.value })} /></Field>
          <Field label="Color de fondo"><input className="input color-input" type="color" value={mapForm.background_color} onChange={e => setMapForm({ ...mapForm, background_color: e.target.value })} /></Field>
          <Field label="Tamaño de cuadrícula"><input className="input" type="number" min={4} max={100} value={mapForm.grid_size} onChange={e => setMapForm({ ...mapForm, grid_size: Number(e.target.value) })} /></Field>
          <Field label="Forma por defecto">
            <select className="input" value={mapForm.default_device_shape} onChange={e => setMapForm({ ...mapForm, default_device_shape: e.target.value })}>
              <option value="circle">Círculo</option>
              <option value="rounded_rect">Rectángulo</option>
              <option value="square">Cuadrado</option>
              <option value="pill">Cápsula</option>
              <option value="diamond">Rombo</option>
              <option value="hexagon">Hexágono</option>
            </select>
          </Field>
          <div />
          <label className="check-label"><input type="checkbox" checked={mapForm.grid_enabled} onChange={e => setMapForm({ ...mapForm, grid_enabled: e.target.checked })} /> Mostrar cuadrícula</label>
          <label className="check-label"><input type="checkbox" checked={mapForm.snap_to_grid} onChange={e => setMapForm({ ...mapForm, snap_to_grid: e.target.checked })} /> Ajustar a cuadrícula</label>
        </div>

        <div className="section-label">Imagen de fondo</div>
        <div className="background-picker">
          <div className="background-preview" style={{ backgroundImage: mapForm.background_image ? `url(${mapForm.background_image})` : undefined }}>
            {!mapForm.background_image && <ImageIcon size={26} />}
          </div>
          <div className="background-actions">
            <label className={`btn btn-secondary btn-sm ${backgroundUploading ? 'disabled' : ''}`}>
              <Upload size={14} /> {backgroundUploading ? 'Subiendo…' : 'Subir imagen'}
              <input
                type="file"
                accept="image/png,image/jpeg,image/webp"
                hidden
                disabled={backgroundUploading}
                onChange={e => { void uploadBackground(e.target.files?.[0] || null); e.target.value = '' }}
              />
            </label>
            {mapForm.background_image && (
              <Button variant="danger" size="sm" onClick={() => setMapForm({ ...mapForm, background_image: '' })}><Trash2 size={14} /> Quitar</Button>
            )}
            <span className="muted small">PNG, JPG o WebP hasta 12 MB. Se sirve desde el volumen <code>map-backgrounds</code>.</span>
          </div>
        </div>
        <Field label={`Opacidad de la imagen · ${Math.round(mapForm.background_opacity * 100)}%`}>
          <input className="input range-input" type="range" min={0} max={1} step={0.05} value={mapForm.background_opacity} onChange={e => setMapForm({ ...mapForm, background_opacity: Number(e.target.value) })} />
        </Field>

        <div className="section-label">Política de polling del mapa</div>
        <div className="form-grid two">
          <Field label="Intervalo por defecto (s)" hint="0 = heredar el intervalo de cada sonda."><input className="input" type="number" min={0} value={mapForm.poll_interval_sec} onChange={e => setMapForm({ ...mapForm, poll_interval_sec: Number(e.target.value) })} /></Field>
          <Field label="Reintentos por defecto" hint="0 = heredar."><input className="input" type="number" min={0} value={mapForm.poll_down_retries} onChange={e => setMapForm({ ...mapForm, poll_down_retries: Number(e.target.value) })} /></Field>
          <Field label="Agente por defecto" hint="Los dispositivos del mapa usarán este agente salvo override."><select className="input" value={mapForm.poll_agent_id} onChange={e => setMapForm({ ...mapForm, poll_agent_id: e.target.value })}><option value="">— Sin agente / core</option>{mapAgents.map(a => <option key={a.id} value={a.id}>{a.name}{a.online ? ' · online' : ' · offline'}</option>)}</select></Field>
          <Field label="Notificaciones"><select className="input" value={mapForm.poll_notifications} onChange={e => setMapForm({ ...mapForm, poll_notifications: Number(e.target.value) })}><option value={-1}>Heredar</option><option value={1}>Activadas</option><option value={0}>Silenciadas</option></select></Field>
        </div>

        <Field label="Descripción"><textarea className="input textarea" rows={2} value={mapForm.description} onChange={e => setMapForm({ ...mapForm, description: e.target.value })} /></Field>
        <Field label="Etiqueta por defecto de dispositivos" hint="Admite [Device.Name], [Device.FirstAddress], etc."><textarea className="input textarea mono" rows={3} value={mapForm.default_device_label} onChange={e => setMapForm({ ...mapForm, default_device_label: e.target.value })} /></Field>
      </Modal>

      <Modal open={exportOpen} onClose={() => setExportOpen(false)} title="Exportar mapa" subtitle="Se exporta la vista actual del lienzo." width={560}
        footer={<Button variant="ghost" onClick={() => setExportOpen(false)}>Cerrar</Button>}>
        <div className="export-grid">
          <button className="export-card" onClick={() => exportImage('png')} disabled={!!exporting}>
            <FileImage size={24} />
            <b>PNG</b>
            <span>Imagen rasterizada a 2×.</span>
          </button>
          <button className="export-card" onClick={() => exportImage('svg')} disabled={!!exporting}>
            <ImageIcon size={24} />
            <b>SVG</b>
            <span>Vectorial, ideal para documentación.</span>
          </button>
          <button className="export-card" onClick={() => exportImage('pdf')} disabled={!!exporting}>
            <FileText size={24} />
            <b>PDF</b>
            <span>Documento listo para imprimir.</span>
          </button>
          <button className="export-card" onClick={exportJSON} disabled={!!exporting}>
            <Download size={24} />
            <b>JSON</b>
            <span>Datos completos del mapa.</span>
          </button>
        </div>
        {exporting && <div className="muted small" style={{ marginTop: 10 }}>Generando {exporting.toUpperCase()}…</div>}
      </Modal>

      <AddNodeModal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        mapId={mapId!}
        currentMapId={mapId!}
        position={addPos}
        defaultShape={data?.map?.default_device_shape as Shape | undefined}
        onCreated={refreshAfterChange}
      />

      <DiscoveryModal
        open={discoveryOpen}
        onClose={() => setDiscoveryOpen(false)}
        mapId={mapId!}
        position={addPos}
        onCreated={refreshAfterChange}
      />

      <LinkDiscoveryModal
        open={linkDiscoveryOpen}
        onClose={() => setLinkDiscoveryOpen(false)}
        mapId={mapId!}
        onCreated={refreshAfterChange}
      />
    </div>
  )
}

export default function MapPage() {
  return (
    <ReactFlowProvider>
      <MapView />
    </ReactFlowProvider>
  )
}
