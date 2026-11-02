import { memo, useRef } from 'react'
import { Handle, Position, type Node, type NodeProps } from '@xyflow/react'
import type { CanvasElement } from '../types'
import { canonicalStatus, statusClass } from '../ui'
import NodeIcon from './NodeIcon'
export type ShapeNodeData = {
  element: CanvasElement
  onOpenSubmap?: (id: string) => void
}

function shapeSvg(shape: string) {
  switch (shape) {
    case 'circle':
      return <ellipse cx="50" cy="50" rx="48.5" ry="48.5" />
    case 'square':
      return <rect x="1.5" y="1.5" width="97" height="97" rx="6" />
    case 'pill':
      return <rect x="1.5" y="1.5" width="97" height="97" rx="48.5" />
    case 'diamond':
      return <polygon points="50,1 99,50 50,99 1,50" />
    case 'hexagon':
      return <polygon points="25,2 75,2 98,50 75,98 25,98 2,50" />
    default:
      return <rect x="1.5" y="1.5" width="97" height="97" rx="12" />
  }
}

function ShapeNode({ data, selected }: NodeProps<Node<ShapeNodeData>>) {
  const element = (data as ShapeNodeData).element
  const openSubmap = (data as ShapeNodeData).onOpenSubmap
  const isSubmap = element.element_type === 'submap'
  const lastPointer = useRef(0)
  const clippedShape = element.shape === 'circle' || element.shape === 'diamond' || element.shape === 'hexagon'
  const status = canonicalStatus(element.status, element.device?.is_acknowledged)
  const lines = (element.rendered_label || '').split('\n').filter(Boolean)
  const title = lines[0] || (isSubmap ? element.submap?.name : element.device?.name) || 'Nodo'
  const sublines = lines.slice(1)
  const summary = element.device?.services_summary
  const issueCount = summary ? (summary.warning || 0) + (summary.down || 0) : 0
  const ack = element.device?.is_acknowledged
  const rootCause = element.device?.root_cause
  const circle = element.shape === 'circle' || element.shape === 'diamond' || element.shape === 'hexagon'
  const compact = element.width < 180 || element.height < 110

  return (
    <div className={`shape-node ${statusClass(status)} shape-${element.shape} ${selected ? 'selected' : ''} ${circle ? 'is-round' : ''} ${compact ? 'is-compact' : ''}`} style={{ transform: `rotate(${Number(element.rotation || 0)}deg)` }} onDoubleClick={e => {
      if (isSubmap && element.target_submap_id && openSubmap) {
        e.stopPropagation()
        openSubmap(element.target_submap_id)
      }
    }} onPointerDown={e => {
      if (!isSubmap || !element.target_submap_id || !openSubmap) return
      const now = performance.now()
      if (now - lastPointer.current < 400) {
        e.stopPropagation()
        openSubmap(element.target_submap_id)
      }
      lastPointer.current = now
    }}>
      <svg className="node-shape-svg" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
        {shapeSvg(element.shape)}
      </svg>

      <div className="node-content">
        {element.show_icon && (
          <div className={`node-icon ${circle ? 'center-icon' : ''}`}>
            <NodeIcon name={element.icon_name} size={44} />
          </div>
        )}
        <div className="node-text">
          <div className="node-title">{title}</div>
          {sublines.slice(0, circle ? 2 : 3).map((line, i) => (
            <div key={i} className={`node-subline ${i === 0 && /\d+\.\d+\.\d+\.\d+/.test(line) ? 'mono' : ''}`}>{line}</div>
          ))}
        </div>
      </div>

      {(ack || issueCount > 0) && (
        <div className={`node-issue-badge ${ack ? 'ack' : statusClass(status)}`}>
          {ack ? 'ACK' : `!${issueCount}`}
        </div>
      )}

      {isSubmap && element.submap && !clippedShape && (
        <div className="submap-footer">
          <span className="sm-up">● {element.submap.counts.up}</span>
          {element.submap.counts.warning > 0 && <span className="sm-warn">● {element.submap.counts.warning}</span>}
          {element.submap.counts.down > 0 && <span className="sm-down">● {element.submap.counts.down}</span>}
          {element.submap.counts.unreachable > 0 && <span className="sm-unreach">● {element.submap.counts.unreachable}</span>}
          <span className="sm-open">abrir →</span>
        </div>
      )}

      {element.element_type === 'device' && element.device && (
        <div className={`node-status-badge ${statusClass(status)}`} title={rootCause ? `Causa raíz: ${rootCause}` : undefined}>
          {status === 'down' ? 'DOWN' : status === 'unreachable' ? 'UPSTREAM' : status === 'warning' ? 'WARN' : status === 'up' ? 'UP' : status.toUpperCase()}
        </div>
      )}

      {rootCause && (
        <div className="node-root-cause" title={`Causa raíz: ${rootCause}`}>↖ {rootCause}</div>
      )}

      <Handle type="target" position={Position.Left} id="center" className="node-handle node-handle-center" />
      <Handle type="source" position={Position.Right} id="center" className="node-handle node-handle-center" />
    </div>
  )
}

export default memo(ShapeNode)
