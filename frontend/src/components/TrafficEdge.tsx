import { createContext, memo, useContext, useState } from 'react'
import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  getSmoothStepPath,
  getStraightPath,
  useViewport,
  type Edge,
  type EdgeProps,
} from '@xyflow/react'
import type { CanvasLink } from '../types'

export type TrafficEdgeData = {
  link: CanvasLink
}

const COLORS: Record<string, string> = {
  normal: '#16a34a',
  warning: '#d97706',
  critical: '#e11d48',
  down: '#dc2626',
}

export const TrafficLabelsContext = createContext(false)

function TrafficEdge({ id, data, selected, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition }: EdgeProps<Edge<TrafficEdgeData>>) {
  const [hover, setHover] = useState(false)
  const showAllSnmp = useContext(TrafficLabelsContext)
  const { zoom } = useViewport()
  const link = (data as TrafficEdgeData).link
  const level = link.status === 'down' ? 'down' : link.color_by_traffic === false ? 'normal' : link.utilization_level || 'normal'
  const color = COLORS[level] || COLORS.normal
  const width = Math.max(1.8, Number(link.thickness || 2.5))
  const style = link.routing_style || 'straight'
  let path = ''
  let labelX = (sourceX + targetX) / 2
  let labelY = (sourceY + targetY) / 2
  if (style === 'orthogonal') {
    ;[path, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 18 })
  } else if (style === 'bezier') {
    ;[path, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, curvature: 0.3 })
  } else {
    ;[path, labelX, labelY] = getStraightPath({ sourceX, sourceY, targetX, targetY })
  }

  const down = link.status === 'down'
  const label = link.rendered_label || (down ? 'LINK DOWN' : '')
  const showLabel = label && (selected || hover || down || zoom >= 1.12 || link.utilization_level !== 'normal' || (showAllSnmp && link.mastering_type === 'snmp' && link.snmp_if_index > 0))

  return (
    <>
      <path d={path} fill="none" stroke={color} strokeOpacity={selected ? 0.16 : 0.08} strokeWidth={width * 5} strokeLinecap="round" />
      <BaseEdge
        id={id}
        path={path}
        style={{
          stroke: color,
          strokeWidth: selected || hover ? width + 1.2 : width,
          strokeDasharray: down || link.dashed ? '7 5' : undefined,
          animation: down ? 'dashmove 1.2s linear infinite' : undefined,
          transition: 'stroke .35s ease, stroke-width .2s ease',
        }}
      />
      <path d={path} fill="none" stroke="transparent" strokeWidth={20} onMouseEnter={() => setHover(true)} onMouseLeave={() => setHover(false)} style={{ cursor: 'pointer' }} />
      {showLabel && (
        <EdgeLabelRenderer>
          <div
            className={`edge-pill ${selected ? 'selected' : ''} ${down ? 'down' : ''}`}
            style={{ transform: `translate(-50%, -100%) translate(${labelX}px, ${labelY - 12}px)` }}
            onMouseEnter={() => setHover(true)}
            onMouseLeave={() => setHover(false)}
          >
            <span className="edge-dot" style={{ background: color }} />
            <span>{label}</span>
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
}

export default memo(TrafficEdge)
