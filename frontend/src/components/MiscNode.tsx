import { memo, useEffect, useRef, useState } from 'react'
import { useReactFlow, type Node, type NodeProps } from '@xyflow/react'
import { apiSend } from '../api'
import type { CanvasElement } from '../types'

type MiscData = { element: CanvasElement; mapId?: string }

function ZoneNode({ data, selected, positionAbsoluteX, positionAbsoluteY }: NodeProps<Node<MiscData>>) {
  const el = data.element
  const style = (el.custom_style || {}) as Record<string, any>
  const color = String(style.color || '#94a3b8')
  const fill = String(style.fill || '#f8fafc')
  const [rotation, setRotation] = useState(Number(el.rotation || 0))
  const rotationRef = useRef(rotation)
  const { flowToScreenPosition } = useReactFlow()
  useEffect(() => { setRotation(Number(el.rotation || 0)) }, [el.rotation])
  const startRotate = (e: React.PointerEvent) => {
    e.stopPropagation()
    e.preventDefault()
    const center = flowToScreenPosition({
      x: positionAbsoluteX + Number(el.width) / 2,
      y: positionAbsoluteY + Number(el.height) / 2,
    })
    const move = (ev: PointerEvent) => {
      const angle = Math.round(Math.atan2(ev.clientY - center.y, ev.clientX - center.x) * 180 / Math.PI + 90)
      rotationRef.current = angle
      setRotation(angle)
    }
    const up = async () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      if (data.mapId) {
        await apiSend(`/maps/${data.mapId}/elements/${el.id}`, 'PATCH', { rotation: rotationRef.current }).catch(() => {})
      }
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }
  return (
    <div
      className={`zone-node ${selected ? 'selected' : ''}`}
      style={{ borderColor: color, background: fill, borderStyle: style.dashed === false ? 'solid' : 'dashed', transform: `rotate(${rotation}deg)` }}
    >
      <div className="zone-title" style={{ color }}>{el.rendered_label || el.label_template || 'Zona'}</div>
      <div className="zone-rotate-handle" title="Arrastrar para rotar" onPointerDown={startRotate} style={{ transform: `rotate(${-rotation}deg)` }}>↻</div>
    </div>
  )
}

function TextNode({ data, selected }: NodeProps<Node<MiscData>>) {
  const el = data.element
  const style = (el.custom_style || {}) as Record<string, any>
  return (
    <div
      className={`text-node ${selected ? 'selected' : ''}`}
      style={{ fontSize: Number(style.fontSize || 12), color: String(style.color || '#475569'), transform: `rotate(${Number(el.rotation || 0)}deg)` }}
    >
      {el.rendered_label || el.label_template}
    </div>
  )
}

export const ZoneNodeMemo = memo(ZoneNode)
export const TextNodeMemo = memo(TextNode)
