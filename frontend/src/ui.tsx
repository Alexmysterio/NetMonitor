import type { ReactNode } from 'react'
import { useEffect, useId, useRef } from 'react'
import { X } from 'lucide-react'
import type { Status } from './types'

export const STATUS_LABEL: Record<Status, string> = {
  up: 'Operativo',
  warning: 'Advertencia',
  down: 'Caído',
  unreachable: 'Inalcanzable',
  acknowledged: 'Reconocido',
  disabled: 'Deshabilitado',
  unknown: 'Desconocido',
}

export function statusClass(status: string): string {
  if (['up', 'warning', 'down', 'unreachable', 'acknowledged', 'disabled', 'unknown'].includes(status)) {
    return `status-${status}`
  }
  return 'status-unknown'
}

export function canonicalStatus(status: string, acknowledged = false): Status {
  if (acknowledged && (status === 'down' || status === 'unreachable')) return 'acknowledged'
  return (status as Status) || 'unknown'
}

export function deviceTypeLabel(type: string): string {
  return (type || 'generic')
    .replace(/_/g, ' ')
    .replace(/\b\w/g, c => c.toUpperCase())
}

export function formatBps(bps: number): string {
  if (!Number.isFinite(bps) || bps <= 0) return '0 bps'
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps']
  let value = bps
  let i = 0
  while (value >= 1000 && i < units.length - 1) {
    value /= 1000
    i++
  }
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(value >= 10 ? 1 : 2)} ${units[i]}`
}

export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  if (seconds < 60) return `${Math.round(seconds)}s`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m ${Math.floor(seconds % 60)}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  const d = Math.floor(h / 24)
  return `${d}d ${h % 24}h`
}

export function timeAgo(iso: string): string {
  if (!iso) return '—'
  const diff = (Date.now() - new Date(iso).getTime()) / 1000
  if (diff < 10) return 'ahora'
  if (diff < 60) return `hace ${Math.floor(diff)}s`
  if (diff < 3600) return `hace ${Math.floor(diff / 60)}m`
  if (diff < 86400) return `hace ${Math.floor(diff / 3600)}h`
  return `hace ${Math.floor(diff / 86400)}d`
}

export function timeHM(iso: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleTimeString('es-ES', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

export function StatusDot({ status, pulse = true, size = 8 }: { status: string; pulse?: boolean; size?: number }) {
  return <span className={`status-dot ${statusClass(status)} ${pulse && status === 'down' ? 'pulsing' : ''}`} style={{ width: size, height: size }} />
}

export function StatusBadge({ status, text, className = '' }: { status: string; text?: string; className?: string }) {
  return (
    <span className={`status-badge ${statusClass(status)} ${className}`}>
      <StatusDot status={status} size={6} />
      {text || STATUS_LABEL[status as Status] || status}
    </span>
  )
}

export function Button({
  children,
  variant = 'secondary',
  size = 'md',
  className = '',
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'dark'; size?: 'sm' | 'md' }) {
  return (
    <button className={`btn btn-${variant} btn-${size} ${className}`} {...props}>
      {children}
    </button>
  )
}

export function IconButton({
  children,
  className = '',
  label,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { label?: string }) {
  return (
    <button className={`icon-btn ${className}`} aria-label={label} title={label} {...props}>
      {children}
    </button>
  )
}

const modalStack: symbol[] = []

export function Modal({
  open,
  onClose,
  title,
  subtitle,
  children,
  footer,
  width = 560,
}: {
  open: boolean
  onClose: () => void
  title: string
  subtitle?: string
  children: ReactNode
  footer?: ReactNode
  width?: number
}) {
  const modalID = useRef<symbol>(Symbol('modal'))
  const panelRef = useRef<HTMLDivElement>(null)
  const titleID = useId()
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose
  useEffect(() => {
    if (!open) return
    const id = modalID.current
    modalStack.push(id)
    const previous = document.activeElement as HTMLElement | null
    window.setTimeout(() => panelRef.current?.focus(), 0)
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && modalStack[modalStack.length - 1] === id) {
        event.preventDefault()
        onCloseRef.current()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      const index = modalStack.lastIndexOf(id)
      if (index >= 0) modalStack.splice(index, 1)
      previous?.focus?.()
    }
  }, [open])
  if (!open) return null
  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <div ref={panelRef} className="modal" style={{ maxWidth: width }} onMouseDown={e => e.stopPropagation()} role="dialog" aria-modal="true" aria-labelledby={titleID} tabIndex={-1}>
        <div className="modal-head">
          <div>
            <h2 id={titleID}>{title}</h2>
            {subtitle && <p>{subtitle}</p>}
          </div>
          <IconButton onClick={onClose} label="Cerrar">
            <X size={18} />
          </IconButton>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
  )
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="field-hint">{hint}</span>}
    </label>
  )
}

export function EmptyState({ title, message, icon }: { title: string; message: string; icon?: ReactNode }) {
  return (
    <div className="empty-state">
      {icon && <div className="empty-icon">{icon}</div>}
      <h3>{title}</h3>
      <p>{message}</p>
    </div>
  )
}

export function Spinner({ label = 'Cargando' }: { label?: string }) {
  return (
    <div className="spinner-wrap">
      <span className="spinner" />
      <span>{label}</span>
    </div>
  )
}

export function KeyValue({ k, v, mono = false }: { k: string; v: ReactNode; mono?: boolean }) {
  return (
    <div className="kv-row">
      <span className="kv-key">{k}</span>
      <span className={`kv-value ${mono ? 'mono' : ''}`}>{v}</span>
    </div>
  )
}
