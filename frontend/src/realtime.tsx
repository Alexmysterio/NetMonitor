import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { apiGet, getToken } from './api'
import type { AlertEvent, NOCSettings } from './types'

export interface RealtimeMessage {
  type: string
  payload?: any
  map_id?: string
  seq: number
}

export interface Toast {
  id: number
  title: string
  message: string
  severity: string
  deviceId?: string
  mapId?: string
  time: string
}

interface RealtimeContextValue {
  connected: boolean
  lastMessage: RealtimeMessage | null
  subscribeMap: (mapId: string) => void
  toasts: Toast[]
  dismissToast: (id: number) => void
  soundEnabled: boolean
  setSoundEnabled: (v: boolean) => void
  refreshKey: number
}

const RealtimeContext = createContext<RealtimeContextValue | null>(null)

function playAlert(severity: string, soundUrl = '') {
  try {
    if (soundUrl) {
      const audio = new Audio(soundUrl)
      audio.volume = 0.75
      void audio.play().catch(() => {})
      return
    }
    const Ctx = window.AudioContext || (window as any).webkitAudioContext
    if (!Ctx) return
    const ctx = new Ctx()
    const sequence = severity === 'critical' ? [660, 440, 660] : severity === 'warning' ? [520, 620] : [780, 980]
    sequence.forEach((freq, i) => {
      const osc = ctx.createOscillator()
      const gain = ctx.createGain()
      osc.type = 'sine'
      osc.frequency.value = freq
      const start = ctx.currentTime + i * 0.18
      gain.gain.setValueAtTime(0.0001, start)
      gain.gain.exponentialRampToValueAtTime(0.12, start + 0.015)
      gain.gain.exponentialRampToValueAtTime(0.0001, start + 0.16)
      osc.connect(gain)
      gain.connect(ctx.destination)
      osc.start(start)
      osc.stop(start + 0.17)
    })
    setTimeout(() => ctx.close().catch(() => {}), 1600)
  } catch {
    /* browser blocked audio */
  }
}

export function RealtimeProvider({ children }: { children: React.ReactNode }) {
  const [connected, setConnected] = useState(false)
  const [lastMessage, setLastMessage] = useState<RealtimeMessage | null>(null)
  const [toasts, setToasts] = useState<Toast[]>([])
  const [refreshKey, setRefreshKey] = useState(0)
  const [soundEnabled, setSoundEnabledState] = useState(() => localStorage.getItem('netmonitor.sound') !== 'off')
  const [nocConfig, setNocConfig] = useState<NOCSettings>({ sound_url: '', popup: false, flash: false })
  const nocRef = useRef(nocConfig)
  nocRef.current = nocConfig
  const wsRef = useRef<WebSocket | null>(null)
  const mapRef = useRef<string>('')
  const reconnectRef = useRef<number | null>(null)
  const activeRef = useRef(false)
  const seqRef = useRef(0)
  const soundRef = useRef(soundEnabled)
  soundRef.current = soundEnabled
  const [sessionToken, setSessionToken] = useState<string | null>(() => getToken())

  const setSoundEnabled = useCallback((v: boolean) => {
    setSoundEnabledState(v)
    localStorage.setItem('netmonitor.sound', v ? 'on' : 'off')
  }, [])

  const connect = useCallback(() => {
    const token = getToken()
    if (!token || !activeRef.current) return
    if (wsRef.current && (wsRef.current.readyState === WebSocket.OPEN || wsRef.current.readyState === WebSocket.CONNECTING)) return
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/ws?token=${encodeURIComponent(token)}`)
    wsRef.current = ws
    ws.onopen = () => {
      setConnected(true)
      if (mapRef.current) ws.send(JSON.stringify({ action: 'SUBSCRIBE_MAP', map_id: mapRef.current }))
    }
    ws.onclose = () => {
      setConnected(false)
      if (wsRef.current === ws) wsRef.current = null
      if (reconnectRef.current) window.clearTimeout(reconnectRef.current)
      if (activeRef.current && getToken()) {
        reconnectRef.current = window.setTimeout(connect, 2500)
      }
    }
    ws.onerror = () => ws.close()
    ws.onmessage = event => {
      let msg: any
      try {
        msg = JSON.parse(event.data)
      } catch {
        return
      }
      const message: RealtimeMessage = { ...msg, seq: ++seqRef.current }
      setLastMessage(message)
      if (msg.type === 'NOC_ALERT_EVENT') {
        const payload = msg.payload as AlertEvent
        if (payload.acknowledged) return
        const id = Date.now() + Math.random()
        setToasts(prev => [
          { id, title: `${payload.device_name}`, message: payload.message || payload.transition, severity: payload.severity, deviceId: payload.device_id, mapId: payload.map_id, time: payload.timestamp },
          ...prev.slice(0, 3),
        ])
        window.setTimeout(() => setToasts(prev => prev.filter(t => t.id !== id)), 8000)
        const config = nocRef.current
        if (soundRef.current) playAlert(payload.severity, config.sound_url)
        if (config.popup && 'Notification' in window && payload.severity !== 'ok') {
          if (Notification.permission === 'granted') {
            new Notification(`${payload.device_name} · ${payload.transition}`, { body: payload.message || payload.map_name || 'Alerta NOC' })
          } else if (Notification.permission === 'default') {
            void Notification.requestPermission()
          }
        }
        if (config.flash && payload.severity === 'critical') {
          document.body.classList.add('noc-flash')
          window.setTimeout(() => document.body.classList.remove('noc-flash'), 2200)
        }
        if (soundRef.current && 'speechSynthesis' in window && payload.severity === 'critical') {
          const text = `Alerta crítica. ${payload.device_name} en ${payload.map_name}.`
          const utter = new SpeechSynthesisUtterance(text)
          utter.lang = 'es-ES'
          utter.rate = 1.02
          window.speechSynthesis.speak(utter)
        }
        setRefreshKey(k => k + 1)
      }
      if (msg.type === 'TRAP_RECEIVED' || msg.type === 'TRAP_SUPPRESSED') {
        setRefreshKey(k => k + 1)
      }
    }
  }, [])

  useEffect(() => {
    const onSession = () => setSessionToken(getToken())
    window.addEventListener('netmonitor:session', onSession)
    return () => window.removeEventListener('netmonitor:session', onSession)
  }, [])

  useEffect(() => {
    activeRef.current = Boolean(sessionToken)
    if (reconnectRef.current) {
      window.clearTimeout(reconnectRef.current)
      reconnectRef.current = null
    }
    if (!sessionToken) {
      const ws = wsRef.current
      wsRef.current = null
      ws?.close()
      setConnected(false)
      return
    }
    apiGet<NOCSettings>('/settings/noc').then(setNocConfig).catch(() => {})
    connect()
    return () => {
      activeRef.current = false
      if (reconnectRef.current) {
        window.clearTimeout(reconnectRef.current)
        reconnectRef.current = null
      }
      const ws = wsRef.current
      wsRef.current = null
      ws?.close()
    }
  }, [sessionToken, connect])

  const subscribeMap = useCallback((mapId: string) => {
    mapRef.current = mapId
    const ws = wsRef.current
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ action: 'SUBSCRIBE_MAP', map_id: mapId }))
    }
  }, [])

  const dismissToast = useCallback((id: number) => setToasts(prev => prev.filter(t => t.id !== id)), [])

  const value = useMemo(
    () => ({ connected, lastMessage, subscribeMap, toasts, dismissToast, soundEnabled, setSoundEnabled, refreshKey }),
    [connected, lastMessage, subscribeMap, toasts, dismissToast, soundEnabled, setSoundEnabled, refreshKey],
  )
  return <RealtimeContext.Provider value={value}>{children}</RealtimeContext.Provider>
}

export function useRealtime() {
  const ctx = useContext(RealtimeContext)
  if (!ctx) throw new Error('useRealtime must be inside RealtimeProvider')
  return ctx
}
