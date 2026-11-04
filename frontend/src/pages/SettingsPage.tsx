import { useEffect, useRef, useState } from 'react'
import { Activity, AlertTriangle, Bell, BellRing, BookOpen, Check, Copy, Globe, KeyRound, Mail, MessageSquare, Pause, Pencil, Play, Plus, Radio, ScrollText, Send, Server, Shield, SlidersHorizontal, Trash2, UserRound, Webhook } from 'lucide-react'
import { apiGet, apiSend, apiUpload, getUser } from '../api'
import { confirmAction } from '../confirm'
import type { Agent, NotificationChannel, SnmpProfile } from '../types'
import { Button, EmptyState, Field, IconButton, Modal, Spinner, StatusBadge } from '../ui'

const DAY_LABELS = ['L', 'M', 'X', 'J', 'V', 'S', 'D']

function scheduleLabel(value: any): string {
  if (!value) return 'Siempre'
  const holidays = Array.isArray(value.holidays) && value.holidays.length ? ` · ${value.holidays.length} festivos` : ''
  if (value.always) return `Siempre${holidays}`
  const days = Array.isArray(value.days) && value.days.length
    ? value.days.map((d: number) => DAY_LABELS[d - 1] || '?').join('')
    : 'Todos los días'
  return `${days} ${value.from || '?'}-${value.to || '?'}${value.timezone ? ` (${value.timezone})` : ''}${holidays}`
}

function toLocalInput(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

const CHANNEL_ICON: Record<string, React.ReactNode> = {
  telegram: <MessageSquare size={17} />,
  email: <Mail size={17} />,
  webhook: <Webhook size={17} />,
  slack: <MessageSquare size={17} />,
  discord: <MessageSquare size={17} />,
  teams: <MessageSquare size={17} />,
  web_audio: <BellRing size={17} />,
  agent_action: <Play size={17} />,
  ntfy: <Radio size={17} />,
  syslog: <Globe size={17} />,
}

function channelSummary(c: NotificationChannel): string {
  const cfg = (c.config || {}) as any
  switch (c.channel_type) {
    case 'telegram': return `Chat ID: ${cfg.chat_id || '-'}`
    case 'email': return `${cfg.host || 'SMTP'} → ${cfg.to || '-'}`
    case 'webhook': return String(cfg.url || 'Webhook configurado')
    case 'slack': case 'discord': case 'teams': case 'ntfy': return 'Destino configurado'
    case 'web_audio': return 'Audio/TTS del navegador NOC'
    case 'agent_action': return `${cfg.action || 'acción'} · ${cfg.agent_id ? 'agente' : 'sin agente'}`
    case 'syslog': return `${cfg.host || '127.0.0.1'}:${cfg.port || 514}`
    case 'pagerduty': case 'opsgenie': case 'sms': return 'Credenciales configuradas'
    default: return 'Canal configurado'
  }
}

type ChannelField = {
  key: string
  label: string
  placeholder?: string
  hint?: string
  type?: string
  required?: boolean
  options?: string[]
}

const CHANNEL_FIELDS: Record<string, ChannelField[]> = {
  email: [
    { key: 'host', label: 'Servidor SMTP', placeholder: 'smtp.example.com', required: true },
    { key: 'port', label: 'Puerto', type: 'number', placeholder: '587' },
    { key: 'from', label: 'Remitente', placeholder: 'netmonitor@example.com', required: true },
    { key: 'to', label: 'Destinatario', placeholder: 'guardia@example.com', required: true },
    { key: 'username', label: 'Usuario SMTP' },
    { key: 'password', label: 'Contraseña SMTP', type: 'password' },
  ],
  webhook: [{ key: 'url', label: 'URL del webhook', placeholder: 'https://example.com/hook', type: 'url', required: true }],
  slack: [{ key: 'url', label: 'Webhook de Slack', placeholder: 'https://hooks.slack.com/services/...', type: 'url', required: true }],
  discord: [{ key: 'url', label: 'Webhook de Discord', placeholder: 'https://discord.com/api/webhooks/...', type: 'url', required: true }],
  teams: [{ key: 'url', label: 'Webhook de Microsoft Teams', placeholder: 'https://...webhook.office.com/...', type: 'url', required: true }],
  ntfy: [{ key: 'url', label: 'URL de ntfy', placeholder: 'https://ntfy.sh/mi-tema', type: 'url', required: true, hint: 'Incluye el tema en la URL.' }],
  pagerduty: [{ key: 'routing_key', label: 'Routing key', placeholder: 'Clave de integración de PagerDuty', required: true }],
  opsgenie: [
    { key: 'api_key', label: 'API key', placeholder: 'Clave de API de Opsgenie', required: true },
    { key: 'region', label: 'Región', options: ['us', 'eu'] },
  ],
  sms: [
    { key: 'account_sid', label: 'Account SID de Twilio', required: true },
    { key: 'auth_token', label: 'Auth token', type: 'password', required: true },
    { key: 'from', label: 'Número remitente', placeholder: '+34...', required: true },
    { key: 'to', label: 'Número destinatario', placeholder: '+34...', required: true },
  ],
  syslog: [
    { key: 'host', label: 'Host Syslog', placeholder: '127.0.0.1', required: true },
    { key: 'port', label: 'Puerto', type: 'number', placeholder: '514' },
  ],
  agent_action: [
    { key: 'agent_id', label: 'Agente', type: 'agent', required: true },
    { key: 'action', label: 'Acción', options: ['icmp', 'tcp', 'http', 'dns', 'ssh', 'smtp', 'ftp'], required: true },
    { key: 'target', label: 'Objetivo', placeholder: 'IP o host', required: true },
    { key: 'port', label: 'Puerto (TCP)', type: 'number' },
  ],
  web_audio: [],
}

const CHANNEL_DEFAULTS: Record<string, Record<string, string>> = {
  email: { port: '587' },
  syslog: { host: '127.0.0.1', port: '514' },
  opsgenie: { region: 'us' },
  agent_action: { action: 'icmp' },
}

function defaultChannelConfig(type: string): Record<string, string> {
  return { ...(CHANNEL_DEFAULTS[type] || {}) }
}

export default function SettingsPage() {
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [profiles, setProfiles] = useState<SnmpProfile[]>([])
  const [snmpRefresh, setSnmpRefresh] = useState({ system_sec: 300, interfaces_sec: 60, ips_sec: 300, arp_sec: 300, routes_sec: 600 })
  const [rules, setRules] = useState<any[]>([])
  const [addressLists, setAddressLists] = useState<any[]>([])
  const [digestRules, setDigestRules] = useState<any[]>([])
  const [deviceTypes, setDeviceTypes] = useState<any[]>([])
  const [probes, setProbes] = useState<any[]>([])
  const [typeOpen, setTypeOpen] = useState(false)
  const [typeEditing, setTypeEditing] = useState<any | null>(null)
  const [typeForm, setTypeForm] = useState({ name: '', label: '', icon: '', shape: 'rounded_rect', color: '#2563eb', description: '', probe_ids: [] as string[] })
  const [digestOpen, setDigestOpen] = useState(false)
  const [digestEditing, setDigestEditing] = useState<any | null>(null)
  const [digestForm, setDigestForm] = useState({ name: '', interval: 60, channels: [] as string[], enabled: true })
  const [loading, setLoading] = useState(true)
  const [tab, setTab] = useState<'channels' | 'noc' | 'snmp' | 'rules' | 'maintenance' | 'groups' | 'types' | 'users' | 'agents' | 'mibs' | 'audit' | 'api_keys'>('channels')
  const [sounds, setSounds] = useState<any[]>([])
  const [nocSettings, setNocSettings] = useState({ sound_url: '', popup: false, flash: false })
  const [soundUploading, setSoundUploading] = useState(false)
  const [channelOpen, setChannelOpen] = useState(false)
  const [profileOpen, setProfileOpen] = useState(false)
  const [ruleOpen, setRuleOpen] = useState(false)
  const [userOpen, setUserOpen] = useState(false)
  const [users, setUsers] = useState<any[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [agentOpen, setAgentOpen] = useState(false)
  const [agentName, setAgentName] = useState('')
  const [agentMax, setAgentMax] = useState(0)
  const [agentToken, setAgentToken] = useState('')
  const [mibs, setMibs] = useState<any[]>([])
  const [auditLogs, setAuditLogs] = useState<any[]>([])
  const [apiKeys, setApiKeys] = useState<any[]>([])
  const [apiKeyOpen, setApiKeyOpen] = useState(false)
  const [apiKeyName, setApiKeyName] = useState('')
  const [apiKeyRole, setApiKeyRole] = useState('viewer')
  const [apiKeyToken, setApiKeyToken] = useState('')
  const [maintenanceWindows, setMaintenanceWindows] = useState<any[]>([])
  const [mwMaps, setMwMaps] = useState<any[]>([])
  const [mwDevices, setMwDevices] = useState<any[]>([])
  const [mwLists, setMwLists] = useState<any[]>([])
  const [mwOpen, setMwOpen] = useState(false)
  const [globalHolidays, setGlobalHolidays] = useState('')
  const [mwForm, setMwForm] = useState({ name: '', scopeType: 'device', scopeID: '', scopeTag: '', starts: '', ends: '', reason: '' })
  const [mibImporting, setMibImporting] = useState(false)
  const [mibResult, setMibResult] = useState('')
  const [mibError, setMibError] = useState('')
  const [testingChannel, setTestingChannel] = useState('')
  const mibRef = useRef<HTMLInputElement>(null)
  const sessionUser = getUser()
  const isAdmin = sessionUser?.role === 'admin'
  const isEditor = sessionUser?.role !== 'viewer'
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [channelForm, setChannelForm] = useState({ name: '', type: 'telegram', enabled: true })
  const [channelConfig, setChannelConfig] = useState<Record<string, string>>({})
  const [telegramToken, setTelegramToken] = useState('')
  const [telegramChat, setTelegramChat] = useState('')
  const [channelEditing, setChannelEditing] = useState<NotificationChannel | null>(null)
  const [profileForm, setProfileForm] = useState({ name: '', version: 'v2c', community: 'public', port: 161, user: '', auth: 'SHA', authPass: '', priv: 'AES', privPass: '' })
  const [profileEditing, setProfileEditing] = useState<SnmpProfile | null>(null)
  const [ruleForm, setRuleForm] = useState({
    name: '', transitions: ['down', 'recovered'], channels: [] as string[], delay_sec: 0, repeat_interval_sec: 0, max_repeats: 0,
    customSchedule: false, days: [1, 2, 3, 4, 5], start: '09:00', end: '18:00', timezone: 'Europe/Madrid',
    holidays: '', groupBy: 'none', groupWindow: 60, enabled: true, addressLists: [] as string[],
  })
  const [ruleEditing, setRuleEditing] = useState<any | null>(null)
  const [userForm, setUserForm] = useState({ username: '', display_name: '', password: '', role: 'operator', restrict_maps: false, restrict_groups: false })
  const [accessOpen, setAccessOpen] = useState(false)
  const [accessUser, setAccessUser] = useState<any>(null)
  const [accessMaps, setAccessMaps] = useState<string[]>([])
  const [allMaps, setAllMaps] = useState<any[]>([])
  const [accessSaving, setAccessSaving] = useState(false)
  const [groups, setGroups] = useState<any[]>([])
  const [groupDevices, setGroupDevices] = useState<any[]>([])
  const [groupOpen, setGroupOpen] = useState(false)
  const [groupEditing, setGroupEditing] = useState<any | null>(null)
  const [groupForm, setGroupForm] = useState({ name: '', description: '', color: '#2563eb', deviceIDs: [] as string[], poll_interval_sec: 0, poll_down_retries: 0, poll_agent_id: '', poll_notifications: -1 })
  const [groupAccessOpen, setGroupAccessOpen] = useState(false)
  const [groupAccessUser, setGroupAccessUser] = useState<any>(null)
  const [userGroupIDs, setUserGroupIDs] = useState<string[]>([])
  const [groupAccessSaving, setGroupAccessSaving] = useState(false)
  const [userDevicesOpen, setUserDevicesOpen] = useState(false)
  const [userDevicesUser, setUserDevicesUser] = useState<any>(null)
  const [userDeviceIDs, setUserDeviceIDs] = useState<string[]>([])
  const [userDeviceList, setUserDeviceList] = useState<any[]>([])
  const [probeTypesOpen, setProbeTypesOpen] = useState(false)
  const [probeTypesUser, setProbeTypesUser] = useState<any>(null)
  const [userProbeTypes, setUserProbeTypes] = useState<string[]>([])
  const [probeTypeSaving, setProbeTypeSaving] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const [c, p, r, d, t, rf, pr, al] = await Promise.all([
        apiGet<NotificationChannel[]>('/notification-channels'),
        apiGet<SnmpProfile[]>('/snmp-profiles'),
        apiGet<any[]>('/alert-rules'),
        apiGet<any[]>('/digest-rules'),
        apiGet<any[]>('/device-types'),
        apiGet<any>('/settings/snmp-refresh'),
        apiGet<any[]>('/probes'),
        apiGet<any[]>('/address-lists').catch(() => []),
      ])
      setChannels(c); setProfiles(p); setRules(r); setDigestRules(d); setDeviceTypes(t); setProbes(pr); setAddressLists(al)
      if (rf) setSnmpRefresh({ system_sec: rf.system_sec, interfaces_sec: rf.interfaces_sec, ips_sec: rf.ips_sec, arp_sec: rf.arp_sec, routes_sec: rf.routes_sec })
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { void load() }, [])
  useEffect(() => {
    if (!isAdmin) return
    apiGet<any[]>('/users').then(setUsers).catch(() => {})
    apiGet<Agent[]>('/agents').then(setAgents).catch(() => {})
    apiGet<any[]>('/api-keys').then(setApiKeys).catch(() => {})
  }, [isAdmin])

  useEffect(() => {
    apiGet<any[]>('/mibs').then(setMibs).catch(() => {})
  }, [])

  useEffect(() => {
    if (tab !== 'noc') return
    Promise.all([apiGet<any[]>('/notification-sounds'), apiGet<any>('/settings/noc')])
      .then(([s, n]) => { setSounds(s); setNocSettings({ sound_url: n.sound_url || '', popup: !!n.popup, flash: !!n.flash }) })
      .catch(() => {})
  }, [tab])

  useEffect(() => {
    if (tab === 'audit' && isAdmin) {
      apiGet<any[]>('/audit').then(setAuditLogs).catch(() => {})
    }
  }, [tab, isAdmin])

  useEffect(() => {
    if (tab === 'groups' && isAdmin) {
      Promise.all([apiGet<any[]>('/device-groups'), apiGet<any[]>('/devices')])
        .then(([g, d]) => { setGroups(g); setGroupDevices(d) })
        .catch(() => {})
    }
  }, [tab, isAdmin])

  useEffect(() => {
    if (tab === 'maintenance' && isEditor) {
      Promise.all([
        apiGet<any[]>('/maintenance-windows'),
        apiGet<any[]>('/maps'),
        apiGet<any[]>('/devices'),
        apiGet<string[]>('/settings/holidays'),
        apiGet<any[]>('/address-lists').catch(() => []),
      ]).then(([windows, maps, devices, holidays, lists]) => {
        setMaintenanceWindows(windows)
        setMwMaps(maps)
        setMwDevices(devices)
        setGlobalHolidays(holidays.join(', '))
        setMwLists(lists)
      }).catch(() => {})
    }
  }, [tab, isEditor])

  const uploadSound = async (file: File | null) => {
    if (!file) return
    setSoundUploading(true); setError('')
    try {
      const form = new FormData()
      form.append('file', file)
      form.append('name', file.name.replace(/\.[^.]+$/, ''))
      await apiUpload('/notification-sounds', form)
      setSounds(await apiGet<any[]>('/notification-sounds'))
    } catch (e) { setError((e as Error).message) } finally { setSoundUploading(false) }
  }

  const saveNOCSettings = async () => {
    setSaving(true); setError('')
    try {
      await apiSend('/settings/noc', 'PUT', nocSettings)
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const removeSound = async (sound: any) => {
    if (!await confirmAction(`¿Eliminar el sonido "${sound.name}"?`)) return
    await apiSend(`/notification-sounds/${sound.id}`, 'DELETE')
    setSounds(await apiGet<any[]>('/notification-sounds'))
    if (nocSettings.sound_url === sound.url) setNocSettings({ ...nocSettings, sound_url: '' })
  }

  const openChannelModal = (channel?: NotificationChannel) => {
    setError('')
    setChannelEditing(channel || null)
    const cfg = (channel?.config || {}) as any
    const type = channel?.channel_type || 'telegram'
    setTelegramToken(type === 'telegram' ? String(cfg.bot_token || '') : '')
    setTelegramChat(type === 'telegram' ? String(cfg.chat_id || '') : '')
    setChannelConfig(Object.fromEntries(
      Object.entries(cfg).map(([key, value]) => [key, value === null || value === undefined ? '' : String(value)]),
    ))
    setChannelForm(channel
      ? { name: channel.name, type, enabled: channel.is_enabled }
      : { name: '', type: 'telegram', enabled: true })
    setChannelOpen(true)
  }

  const saveChannel = async () => {
    setError(''); setSaving(true)
    try {
      let config: Record<string, unknown>
      if (channelForm.type === 'telegram') {
        const token = telegramToken.trim()
        const chat = telegramChat.trim()
        if (!token || !chat) throw new Error('Bot token y chat ID son obligatorios')
        config = { ...((channelEditing?.config || {}) as Record<string, unknown>), bot_token: token, chat_id: chat }
      } else {
        config = { ...((channelEditing?.config || {}) as Record<string, unknown>) }
        const fields = CHANNEL_FIELDS[channelForm.type] || []
        for (const field of fields) {
          const raw = (channelConfig[field.key] || '').trim()
          if (!raw) {
            if (field.required) throw new Error(`${field.label} es obligatorio`)
            delete config[field.key]
            continue
          }
          config[field.key] = field.type === 'number' ? Number(raw) : raw
        }
      }
      const payload = { name: channelForm.name, channel_type: channelForm.type, config, is_enabled: channelForm.enabled }
      if (channelEditing) await apiSend(`/notification-channels/${channelEditing.id}`, 'PUT', payload)
      else await apiSend('/notification-channels', 'POST', payload)
      setChannelOpen(false); setChannelEditing(null); await load()
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const saveSNMPRefresh = async () => {
    setSaving(true); setError('')
    try {
      const saved = await apiSend<any>('/settings/snmp-refresh', 'PUT', snmpRefresh)
      setSnmpRefresh(saved)
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const toggleChannel = async (channel: NotificationChannel) => {
    await apiSend(`/notification-channels/${channel.id}`, 'PUT', { is_enabled: !channel.is_enabled })
    await load()
  }

  const deleteChannel = async (channel: NotificationChannel) => {
    if (!await confirmAction(`¿Eliminar el canal "${channel.name}"? También se quitará de las reglas que lo usen.`)) return
    await apiSend(`/notification-channels/${channel.id}`, 'DELETE')
    await load()
  }

  const setupTelegram = async (channel: NotificationChannel) => {
    try {
      const data = await apiSend<{ webhook_url: string }>(`/notification-channels/${channel.id}/telegram-webhook`, 'POST')
      alert(`Botones de Telegram activados.\nWebhook: ${data.webhook_url}`)
    } catch (e) {
      alert((e as Error).message)
    }
  }

  const openProfileModal = (profile?: SnmpProfile) => {
    setError('')
    setProfileEditing(profile || null)
    setProfileForm(profile
      ? { name: profile.name, version: profile.version, community: profile.community, port: profile.port,
          user: profile.v3_username || '', auth: 'SHA', authPass: '', priv: 'AES', privPass: '' }
      : { name: '', version: 'v2c', community: 'public', port: 161, user: '', auth: 'SHA', authPass: '', priv: 'AES', privPass: '' })
    setProfileOpen(true)
  }

  const saveProfile = async () => {
    setError(''); setSaving(true)
    try {
      const payload = {
        name: profileForm.name, version: profileForm.version, community: profileForm.community, port: profileForm.port,
        v3_username: profileForm.user, v3_security_level: profileForm.version === 'v3' ? 'authPriv' : '',
        v3_auth_protocol: profileForm.auth, v3_auth_pass: profileForm.authPass,
        v3_priv_protocol: profileForm.priv, v3_priv_pass: profileForm.privPass,
      }
      if (profileEditing) await apiSend(`/snmp-profiles/${profileEditing.id}`, 'PUT', payload)
      else await apiSend('/snmp-profiles', 'POST', payload)
      setProfileOpen(false); setProfileEditing(null); await load()
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const deleteProfile = async (profile: SnmpProfile) => {
    if (!await confirmAction(`¿Eliminar el perfil SNMP "${profile.name}"? Los dispositivos que lo usen quedarán sin perfil.`)) return
    await apiSend(`/snmp-profiles/${profile.id}`, 'DELETE')
    await load()
  }

  const openRuleModal = (rule?: any) => {
    setError('')
    setRuleEditing(rule || null)
    setRuleForm(rule
      ? {
          name: rule.name,
          transitions: rule.trigger_transitions || ['down', 'recovered'],
          channels: rule.channel_ids || [],
          delay_sec: rule.delay_sec || 0,
          repeat_interval_sec: rule.repeat_interval_sec || 0,
          max_repeats: rule.max_repeats || 0,
          customSchedule: !(rule.active_schedule?.always ?? true),
          days: rule.active_schedule?.days || [1, 2, 3, 4, 5],
          start: rule.active_schedule?.from || '09:00',
          end: rule.active_schedule?.to || '18:00',
          timezone: rule.active_schedule?.timezone || 'Europe/Madrid',
          holidays: (rule.active_schedule?.holidays || []).join(', '),
          groupBy: rule.group_by || 'none',
          groupWindow: rule.group_window_sec || 60,
          enabled: rule.is_enabled !== false,
          addressLists: rule.filter_address_list_ids || [],
        }
      : {
          name: '', transitions: ['down', 'recovered'], channels: [], delay_sec: 0, repeat_interval_sec: 0, max_repeats: 0,
          customSchedule: false, days: [1, 2, 3, 4, 5], start: '09:00', end: '18:00', timezone: 'Europe/Madrid',
          holidays: '', groupBy: 'none', groupWindow: 60, enabled: true, addressLists: [],
        })
    setRuleOpen(true)
  }

  const saveRule = async () => {
    setError(''); setSaving(true)
    try {
      const holidays = ruleForm.holidays.split(',').map(s => s.trim()).filter(Boolean)
      const payload = {
        name: ruleForm.name, trigger_transitions: ruleForm.transitions,
        channel_ids: ruleForm.channels,
        delay_sec: ruleForm.delay_sec,
        repeat_interval_sec: ruleForm.repeat_interval_sec,
        max_repeats: ruleForm.max_repeats,
        active_schedule: ruleForm.customSchedule
          ? {
              days: ruleForm.days, from: ruleForm.start, to: ruleForm.end, timezone: ruleForm.timezone,
              ...(holidays.length ? { holidays } : {}),
            }
          : { always: true, ...(holidays.length ? { holidays } : {}) },
        group_by: ruleForm.groupBy,
        group_window_sec: ruleForm.groupWindow,
        filter_address_list_ids: ruleForm.addressLists,
        is_enabled: ruleForm.enabled,
      }
      if (ruleEditing) await apiSend(`/alert-rules/${ruleEditing.id}`, 'PUT', payload)
      else await apiSend('/alert-rules', 'POST', payload)
      setRuleOpen(false); setRuleEditing(null); await load()
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const toggleRule = async (rule: any) => {
    await apiSend(`/alert-rules/${rule.id}`, 'PUT', {
      name: rule.name, priority: rule.priority, trigger_transitions: rule.trigger_transitions,
      channel_ids: rule.channel_ids, delay_sec: rule.delay_sec,
      repeat_interval_sec: rule.repeat_interval_sec, max_repeats: rule.max_repeats,
      active_schedule: rule.active_schedule || { always: true },
      group_by: rule.group_by || 'none', group_window_sec: rule.group_window_sec || 60,
      is_enabled: !rule.is_enabled,
    })
    await load()
  }

  const deleteRule = async (rule: any) => {
    if (!await confirmAction(`¿Eliminar la regla "${rule.name}"?`)) return
    await apiSend(`/alert-rules/${rule.id}`, 'DELETE')
    await load()
  }

  const openDigestModal = (rule?: any) => {
    setError('')
    setDigestEditing(rule || null)
    setDigestForm(rule
      ? { name: rule.name, interval: Math.round(rule.interval_sec / 60), channels: rule.channel_ids || [], enabled: rule.is_enabled }
      : { name: 'Resumen de guardia', interval: 60, channels: [], enabled: true })
    setDigestOpen(true)
  }

  const saveDigest = async () => {
    setError(''); setSaving(true)
    try {
      const payload = { name: digestForm.name, interval_sec: Math.max(60, digestForm.interval * 60), channel_ids: digestForm.channels, is_enabled: digestForm.enabled }
      if (digestEditing) await apiSend(`/digest-rules/${digestEditing.id}`, 'PUT', payload)
      else await apiSend('/digest-rules', 'POST', payload)
      setDigestOpen(false); setDigestEditing(null)
      setDigestRules(await apiGet<any[]>('/digest-rules'))
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const toggleDigest = async (rule: any) => {
    await apiSend(`/digest-rules/${rule.id}`, 'PUT', {
      name: rule.name, interval_sec: rule.interval_sec, channel_ids: rule.channel_ids, is_enabled: !rule.is_enabled,
    })
    setDigestRules(await apiGet<any[]>('/digest-rules'))
  }

  const deleteDigest = async (rule: any) => {
    if (!await confirmAction(`¿Eliminar el resumen "${rule.name}"?`)) return
    await apiSend(`/digest-rules/${rule.id}`, 'DELETE')
    setDigestRules(await apiGet<any[]>('/digest-rules'))
  }

  const openTypeModal = (typ?: any) => {
    setError('')
    setTypeEditing(typ || null)
    setTypeForm(typ
      ? { name: typ.name, label: typ.label, icon: typ.icon || '', shape: typ.shape || 'rounded_rect', color: typ.color || '#2563eb', description: typ.description || '', probe_ids: typ.probe_ids || [] }
      : { name: '', label: '', icon: '', shape: 'rounded_rect', color: '#2563eb', description: '', probe_ids: [] })
    setTypeOpen(true)
  }

  const saveType = async () => {
    setError(''); setSaving(true)
    try {
      const payload = { ...typeForm }
      if (typeEditing) await apiSend(`/device-types/${typeEditing.id}`, 'PUT', payload)
      else await apiSend('/device-types', 'POST', payload)
      setTypeOpen(false); setTypeEditing(null)
      setDeviceTypes(await apiGet<any[]>('/device-types'))
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const deleteType = async (typ: any) => {
    if (!await confirmAction(`¿Eliminar el tipo "${typ.label}"?`)) return
    await apiSend(`/device-types/${typ.id}`, 'DELETE')
    setDeviceTypes(await apiGet<any[]>('/device-types'))
  }

  const createAPIKey = async () => {
    setError(''); setSaving(true)
    try {
      const data = await apiSend<{ token: string }>('/api-keys', 'POST', { name: apiKeyName, role: apiKeyRole })
      setApiKeyToken(data.token)
      setApiKeys(await apiGet<any[]>('/api-keys'))
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const toggleAPIKey = async (key: any) => {
    await apiSend(`/api-keys/${key.id}`, 'PUT', { is_enabled: !key.is_enabled })
    setApiKeys(await apiGet<any[]>('/api-keys'))
  }

  const deleteAPIKey = async (key: any) => {
    if (!await confirmAction(`¿Eliminar la API key "${key.name}"? Las integraciones que la usen dejarán de funcionar.`)) return
    await apiSend(`/api-keys/${key.id}`, 'DELETE')
    setApiKeys(await apiGet<any[]>('/api-keys'))
  }

  const openMaintenanceModal = () => {
    const now = new Date()
    setError('')
    setMwForm({ name: '', scopeType: 'device', scopeID: '', scopeTag: '', starts: toLocalInput(now), ends: toLocalInput(new Date(now.getTime() + 3600000)), reason: '' })
    setMwOpen(true)
  }

  const createMaintenanceWindow = async () => {
    setError(''); setSaving(true)
    try {
      await apiSend('/maintenance-windows', 'POST', {
        name: mwForm.name,
        scope_type: mwForm.scopeType,
        scope_id: mwForm.scopeType === 'tag' ? '' : mwForm.scopeID,
        scope_tag: mwForm.scopeType === 'tag' ? mwForm.scopeTag : '',
        starts_at: new Date(mwForm.starts).toISOString(),
        ends_at: new Date(mwForm.ends).toISOString(),
        reason: mwForm.reason,
      })
      setMwOpen(false)
      setMaintenanceWindows(await apiGet<any[]>('/maintenance-windows'))
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const deleteMaintenanceWindow = async (mw: any) => {
    if (!await confirmAction(`¿Eliminar la ventana "${mw.name}"?`)) return
    await apiSend(`/maintenance-windows/${mw.id}`, 'DELETE')
    setMaintenanceWindows(await apiGet<any[]>('/maintenance-windows'))
  }

  const saveGlobalHolidays = async () => {
    const holidays = globalHolidays.split(',').map(s => s.trim()).filter(Boolean)
    const saved = await apiSend<string[]>('/settings/holidays', 'PUT', { holidays })
    setGlobalHolidays(saved.join(', '))
    alert('Calendario global guardado')
  }

  const openGroupModal = (group?: any) => {
    setError('')
    setGroupEditing(group || null)
    setGroupForm(group
      ? { name: group.name, description: group.description || '', color: group.color || '#2563eb', deviceIDs: group.device_ids || [], poll_interval_sec: group.poll_interval_sec || 0, poll_down_retries: group.poll_down_retries || 0, poll_agent_id: group.poll_agent_id || '', poll_notifications: group.poll_notifications ?? -1 }
      : { name: '', description: '', color: '#2563eb', deviceIDs: [], poll_interval_sec: 0, poll_down_retries: 0, poll_agent_id: '', poll_notifications: -1 })
    setGroupOpen(true)
  }

  const saveGroup = async () => {
    setError(''); setSaving(true)
    try {
      const payload = { name: groupForm.name, description: groupForm.description, color: groupForm.color, device_ids: groupForm.deviceIDs, poll_interval_sec: groupForm.poll_interval_sec, poll_down_retries: groupForm.poll_down_retries, poll_agent_id: groupForm.poll_agent_id, poll_notifications: groupForm.poll_notifications }
      if (groupEditing) await apiSend(`/device-groups/${groupEditing.id}`, 'PUT', payload)
      else await apiSend('/device-groups', 'POST', payload)
      setGroupOpen(false); setGroupEditing(null)
      setGroups(await apiGet<any[]>('/device-groups'))
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const deleteGroup = async (group: any) => {
    if (!await confirmAction(`¿Eliminar el grupo "${group.name}"? Los usuarios asignados dejarán de ver sus dispositivos.`)) return
    await apiSend(`/device-groups/${group.id}`, 'DELETE')
    setGroups(await apiGet<any[]>('/device-groups'))
  }

  const openUserGroups = async (user: any) => {
    const [selected, all] = await Promise.all([apiGet<string[]>(`/users/${user.id}/groups`), apiGet<any[]>('/device-groups')])
    setGroups(all)
    setGroupAccessUser(user)
    setUserGroupIDs(selected)
    setGroupAccessOpen(true)
  }

  const saveUserGroups = async () => {
    if (!groupAccessUser) return
    setGroupAccessSaving(true)
    try {
      await apiSend(`/users/${groupAccessUser.id}/groups`, 'PUT', { group_ids: userGroupIDs })
      await apiSend(`/users/${groupAccessUser.id}`, 'PUT', { restrict_groups: true })
      setGroupAccessOpen(false)
      setUsers(await apiGet<any[]>('/users'))
    } finally { setGroupAccessSaving(false) }
  }

  const openUserDevices = async (user: any) => {
    const [selected, all] = await Promise.all([apiGet<string[]>(`/users/${user.id}/devices`), apiGet<any[]>('/devices')])
    setUserDevicesUser(user)
    setUserDeviceIDs(selected)
    setUserDeviceList(all)
    setUserDevicesOpen(true)
  }

  const saveUserDevices = async () => {
    if (!userDevicesUser) return
    await apiSend(`/users/${userDevicesUser.id}/devices`, 'PUT', { device_ids: userDeviceIDs })
    await apiSend(`/users/${userDevicesUser.id}`, 'PUT', { restrict_devices: true })
    setUserDevicesOpen(false)
    setUsers(await apiGet<any[]>('/users'))
  }

  const openProbeTypes = (user: any) => {
    setProbeTypesUser(user)
    setUserProbeTypes(user.allowed_probe_types || [])
    setProbeTypesOpen(true)
  }

  const saveProbeTypes = async () => {
    if (!probeTypesUser) return
    setProbeTypeSaving(true)
    try {
      await apiSend(`/users/${probeTypesUser.id}`, 'PUT', { allowed_probe_types: userProbeTypes })
      setProbeTypesOpen(false)
      setUsers(await apiGet<any[]>('/users'))
    } finally { setProbeTypeSaving(false) }
  }

  const testChannel = async (id: string) => {
    setTestingChannel(id)
    try {
      await apiSend(`/notification-channels/${id}/test`, 'POST')
      alert('Prueba enviada correctamente')
    } catch (e) {
      alert((e as Error).message)
    } finally {
      setTestingChannel('')
    }
  }

  const createUser = async () => {
    setError(''); setSaving(true)
    try {
      await apiSend('/users', 'POST', userForm)
      setUserOpen(false)
      setUserForm({ username: '', display_name: '', password: '', role: 'operator', restrict_maps: false, restrict_groups: false })
      setUsers(await apiGet<any[]>('/users'))
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const deleteUser = async (id: string) => {
    if (!await confirmAction('¿Eliminar este usuario?')) return
    await apiSend(`/users/${id}`, 'DELETE')
    setUsers(await apiGet<any[]>('/users'))
  }

  const openUserMaps = async (u: any) => {
    setAccessUser(u)
    setAccessOpen(true)
    const [maps, selected] = await Promise.all([
      apiGet<any[]>('/maps'),
      apiGet<string[]>(`/users/${u.id}/maps`),
    ])
    setAllMaps(maps)
    setAccessMaps(selected)
  }

  const saveUserMaps = async () => {
    if (!accessUser) return
    setAccessSaving(true)
    try {
      await apiSend(`/users/${accessUser.id}/maps`, 'PUT', { map_ids: accessMaps })
      setAccessOpen(false)
      setUsers(await apiGet<any[]>('/users'))
    } finally {
      setAccessSaving(false)
    }
  }

  const createAgent = async () => {
    setError(''); setSaving(true)
    try {
      const data = await apiSend<{ token: string }>('/agents', 'POST', { name: agentName, max_devices: agentMax })
      setAgentToken(data.token)
      setAgents(await apiGet<Agent[]>('/agents'))
    } catch (e) { setError((e as Error).message) } finally { setSaving(false) }
  }

  const setAgentQuota = async (agent: Agent) => {
    const raw = prompt('Máximo de dispositivos (0 = sin límite):', String(agent.max_devices || 0))
    if (raw === null) return
    const max = Number(raw)
    if (Number.isNaN(max)) return
    await apiSend(`/agents/${agent.id}`, 'PUT', { max_devices: Math.max(0, max) })
    setAgents(await apiGet<Agent[]>('/agents'))
  }

  const updateAgent = async (agent: Agent) => {
    const url = prompt('URL del binario firmado del agente:')
    if (!url) return
    const sha256 = prompt('SHA-256 hexadecimal del binario:')
    if (!sha256) return
    const signature = prompt('Firma Ed25519 (base64):')
    if (!signature) return
    const version = prompt('Versión (opcional):', '') || ''
    await apiSend(`/agents/${agent.id}/update`, 'POST', { url, sha256, signature, version })
    alert('Actualización enviada. El agente se reiniciará tras verificarla.')
  }

  const publishAgentRelease = async () => {
    const version = prompt('Versión de la release:')
    if (!version) return
    const channel = (prompt('Canal (stable/beta):', 'stable') || 'stable').toLowerCase()
    const url = prompt('URL del binario:')
    if (!url) return
    const sha256 = prompt('SHA-256:')
    if (!sha256) return
    const signature = prompt('Firma Ed25519 (base64):')
    if (!signature) return
    await apiSend('/agent-releases', 'POST', { version, channel, url, sha256, signature })
    alert('Release publicada')
  }

  const batchDeployAgents = async () => {
    const channel = (prompt('Canal a desplegar (stable/beta):', 'stable') || 'stable').toLowerCase()
    if (!await confirmAction(`¿Desplegar la última release ${channel} a todos los agentes habilitados?`)) return
    const result = await apiSend<any>('/agents/update', 'POST', { channel })
    alert(`Release ${result.version}: ${result.sent} enviados, ${result.failed} fallidos`)
  }

  const deleteAgent = async (id: string) => {
    if (!await confirmAction('¿Eliminar este agente? Los dispositivos asignados volverán al sondeo central.')) return
    await apiSend(`/agents/${id}`, 'DELETE')
    setAgents(await apiGet<Agent[]>('/agents'))
  }

  const importMIB = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || [])
    event.target.value = ''
    if (!files.length) return
    setMibError(''); setMibResult(''); setMibImporting(true)
    try {
      const modules = await Promise.all(files.map(async file => ({
        module: file.name.replace(/\.(mib|txt|my)$/i, ''),
        content: await file.text(),
      })))
      const data = await apiSend<{ modules: Record<string, number> }>('/mibs/import-batch', 'POST', { modules })
      const total = Object.values(data.modules).reduce((a, b) => a + b, 0)
      setMibResult(`${Object.keys(data.modules).length} módulos · ${total} objetos importados`)
      setMibs(await apiGet<any[]>('/mibs'))
    } catch (e) {
      setMibError((e as Error).message)
    } finally {
      setMibImporting(false)
    }
  }

  const deleteMIB = async (module: string) => {
    if (!await confirmAction(`¿Eliminar el módulo MIB ${module}?`)) return
    await apiSend(`/mibs/${encodeURIComponent(module)}`, 'DELETE')
    setMibs(await apiGet<any[]>('/mibs'))
  }

  if (loading) return <div className="page-center"><Spinner /></div>

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <span className="eyebrow">Configuración</span>
          <h1>Ajustes y avisos</h1>
          <p className="page-subtitle">Canales de notificación, credenciales SNMP y reglas de escalado.</p>
        </div>
        <div className="page-head-actions">
          {tab === 'rules' && <Button variant="secondary" onClick={() => openRuleModal()}><Bell size={15} /> Nueva regla</Button>}
          {tab === 'channels' && <Button variant="primary" onClick={() => openChannelModal()}><Plus size={15} /> Nuevo canal</Button>}
        </div>
      </div>

      <div className="segmented page-segmented">
        <button className={tab === 'channels' ? 'active' : ''} onClick={() => setTab('channels')}><Bell size={15} /> Canales <span>{channels.length}</span></button>
        <button className={tab === 'noc' ? 'active' : ''} onClick={() => setTab('noc')}><BellRing size={15} /> NOC y sonidos</button>
        <button className={tab === 'snmp' ? 'active' : ''} onClick={() => setTab('snmp')}><KeyRound size={15} /> Perfiles SNMP <span>{profiles.length}</span></button>
        <button className={tab === 'rules' ? 'active' : ''} onClick={() => setTab('rules')}><SlidersHorizontal size={15} /> Reglas <span>{rules.length}</span></button>
        {isEditor && <button className={tab === 'maintenance' ? 'active' : ''} onClick={() => setTab('maintenance')}><Pause size={15} /> Mantenimiento <span>{maintenanceWindows.length}</span></button>}
        {isAdmin && <button className={tab === 'groups' ? 'active' : ''} onClick={() => setTab('groups')}><UserRound size={15} /> Grupos <span>{groups.length}</span></button>}
        {isAdmin && <button className={tab === 'types' ? 'active' : ''} onClick={() => setTab('types')}><Server size={15} /> Tipos <span>{deviceTypes.length}</span></button>}
        {isAdmin && <button className={tab === 'users' ? 'active' : ''} onClick={() => setTab('users')}><UserRound size={15} /> Usuarios <span>{users.length}</span></button>}
        {isAdmin && <button className={tab === 'agents' ? 'active' : ''} onClick={() => setTab('agents')}><Server size={15} /> Agentes <span>{agents.length}</span></button>}
        <button className={tab === 'mibs' ? 'active' : ''} onClick={() => setTab('mibs')}><BookOpen size={15} /> MIBs <span>{mibs.length}</span></button>
        {isAdmin && <button className={tab === 'api_keys' ? 'active' : ''} onClick={() => setTab('api_keys')}><KeyRound size={15} /> API keys <span>{apiKeys.length}</span></button>}
        {isAdmin && <button className={tab === 'audit' ? 'active' : ''} onClick={() => setTab('audit')}><ScrollText size={15} /> Auditoría</button>}
      </div>

      {tab === 'channels' && (
        <div className="settings-grid">
          {channels.map(c => (
            <div className="settings-card" key={c.id}>
              <div className="settings-icon">{CHANNEL_ICON[c.channel_type] || <Bell size={17} />}</div>
              <div className="settings-card-main">
                <div className="settings-card-title"><b>{c.name}</b><StatusBadge status={c.is_enabled ? 'up' : 'disabled'} text={c.is_enabled ? 'Activo' : 'Pausado'} /></div>
                <span className="settings-type">{c.channel_type}</span>
                <code>{channelSummary(c)}</code>
                <div className="settings-card-actions">
                  <Button variant="secondary" size="sm" onClick={() => testChannel(c.id)} disabled={testingChannel === c.id}>
                    <Send size={13} /> {testingChannel === c.id ? 'Enviando…' : 'Probar'}
                  </Button>
                  {c.channel_type === 'telegram' && <Button variant="secondary" size="sm" title="Activa botones interactivos (Ack, silenciar, ping). Requiere PUBLIC_URL HTTPS pública." onClick={() => setupTelegram(c)}><MessageSquare size={13} /> Botones</Button>}
                  <Button variant="secondary" size="sm" onClick={() => toggleChannel(c)}>
                    {c.is_enabled ? <Pause size={13} /> : <Play size={13} />} {c.is_enabled ? 'Pausar' : 'Activar'}
                  </Button>
                  <Button variant="secondary" size="sm" aria-label="Editar canal" onClick={() => openChannelModal(c)}><Pencil size={13} /></Button>
                  <Button variant="danger" size="sm" aria-label="Eliminar canal" onClick={() => deleteChannel(c)}><Trash2 size={13} /></Button>
                </div>
              </div>
            </div>
          ))}
          {!channels.length && <EmptyState title="Sin canales" message="Añade un canal para recibir avisos." icon={<Bell size={24} />} />}
        </div>
      )}

      {tab === 'noc' && (
        <div className="noc-settings-grid">
          <div className="settings-card">
            <div className="settings-card-head"><div className="settings-icon"><BellRing size={18} /></div><div><b>Popups y flash</b><span>Se aplican a las alertas NOC del navegador.</span></div></div>
            <label className="check-label"><input type="checkbox" checked={nocSettings.popup} onChange={e => setNocSettings({ ...nocSettings, popup: e.target.checked })} /> Mostrar notificación de escritorio</label>
            <label className="check-label"><input type="checkbox" checked={nocSettings.flash} onChange={e => setNocSettings({ ...nocSettings, flash: e.target.checked })} /> Destello visual al recibir una alerta</label>
            <Field label="Sonido de alerta" hint="Si no eliges sonido, se usa el tono sintetizado.">
              <select className="input" value={nocSettings.sound_url} onChange={e => setNocSettings({ ...nocSettings, sound_url: e.target.value })}>
                <option value="">— Tono sintetizado</option>
                {sounds.map(s => <option key={s.id} value={s.url}>{s.name}</option>)}
              </select>
            </Field>
            <Button variant="primary" onClick={saveNOCSettings} disabled={saving}>{saving ? 'Guardando…' : 'Guardar preferencias NOC'}</Button>
          </div>
          <div className="settings-card">
            <div className="settings-card-head"><div className="settings-icon"><Play size={18} /></div><div><b>Sonidos propios</b><span>MP3, WAV, OGG, WebM o M4A hasta 8 MB.</span></div></div>
            <label className={`btn btn-secondary btn-sm ${soundUploading ? 'disabled' : ''}`}>
              {soundUploading ? 'Subiendo…' : 'Subir sonido'}
              <input type="file" accept="audio/*,.mp3,.wav,.ogg,.m4a,.webm" hidden onChange={e => { void uploadSound(e.target.files?.[0] || null); e.target.value = '' }} />
            </label>
            <div className="sound-list">
              {sounds.map(s => (
                <div className="sound-row" key={s.id}>
                  <span><Play size={12} /> {s.name}</span>
                  <audio controls src={s.url} preload="none" />
                  <Button variant="danger" size="sm" onClick={() => removeSound(s)}><Trash2 size={12} /></Button>
                </div>
              ))}
              {!sounds.length && <div className="muted small">Sin sonidos subidos.</div>}
            </div>
          </div>
        </div>
      )}

      {tab === 'snmp' && (
        <div className="settings-grid">
          <div className="settings-card" style={{ gridColumn: '1 / -1' }}>
            <div className="settings-icon purple"><Shield size={17} /></div>
            <div className="settings-card-main">
              <div className="settings-card-title"><b>Refresco SNMP global</b></div>
              <span className="settings-type">Los dispositivos sin intervalos propios heredan estos valores. `0` = solo manual.</span>
              <div className="form-grid two" style={{ marginTop: 8 }}>
                <Field label="Sistema (s)"><input className="input" type="number" min={0} max={86400} value={snmpRefresh.system_sec} onChange={e => setSnmpRefresh({ ...snmpRefresh, system_sec: Number(e.target.value) })} /></Field>
                <Field label="Interfaces (s)"><input className="input" type="number" min={0} max={86400} value={snmpRefresh.interfaces_sec} onChange={e => setSnmpRefresh({ ...snmpRefresh, interfaces_sec: Number(e.target.value) })} /></Field>
                <Field label="IPs (s)"><input className="input" type="number" min={0} max={86400} value={snmpRefresh.ips_sec} onChange={e => setSnmpRefresh({ ...snmpRefresh, ips_sec: Number(e.target.value) })} /></Field>
                <Field label="ARP (s)"><input className="input" type="number" min={0} max={86400} value={snmpRefresh.arp_sec} onChange={e => setSnmpRefresh({ ...snmpRefresh, arp_sec: Number(e.target.value) })} /></Field>
                <Field label="Rutas (s)"><input className="input" type="number" min={0} max={86400} value={snmpRefresh.routes_sec} onChange={e => setSnmpRefresh({ ...snmpRefresh, routes_sec: Number(e.target.value) })} /></Field>
              </div>
              <div className="settings-card-actions"><Button variant="primary" size="sm" onClick={saveSNMPRefresh} disabled={saving}>Guardar intervalos</Button></div>
            </div>
          </div>
          {profiles.map(p => (
            <div className="settings-card" key={p.id}>
              <div className="settings-icon purple"><Shield size={17} /></div>
              <div className="settings-card-main">
                <div className="settings-card-title"><b>{p.name}</b>{p.is_default && <span className="builtin-pill">default</span>}</div>
                <span className="settings-type">{p.version} · puerto {p.port} · timeout {p.timeout_ms} ms</span>
                <code>community: {p.version === 'v3' ? `user ${p.v3_username} (${p.v3_security_level})` : p.community}</code>
                <div className="settings-card-actions">
                  <Button variant="secondary" size="sm" onClick={() => openProfileModal(p)}><Pencil size={13} /> Editar</Button>
                  <Button variant="danger" size="sm" onClick={() => deleteProfile(p)}><Trash2 size={13} /></Button>
                </div>
              </div>
            </div>
          ))}
          <button className="settings-card add-card" onClick={() => openProfileModal()}><Plus size={18} /><span>Nuevo perfil SNMP</span></button>
        </div>
      )}

      {tab === 'groups' && isAdmin && (
        <div className="rules-list">
          <div className="table-toolbar">
            <span className="muted small">Agrupa dispositivos para asignar permisos por grupo a usuarios `operator` o `viewer`.</span>
            <Button variant="primary" size="sm" onClick={() => openGroupModal()}><Plus size={14} /> Nuevo grupo</Button>
          </div>
          {groups.map(g => (
            <div className="rule-row" key={g.id}>
              <span className="map-tree-status" style={{ background: g.color || '#2563eb', width: 12, height: 12, borderRadius: 99 }} />
              <div className="rule-main"><b>{g.name}</b><span>{g.description || 'Sin descripción'} · {g.device_count} dispositivos</span></div>
              <Button variant="secondary" size="sm" onClick={() => openGroupModal(g)}><Pencil size={13} /> Editar</Button>
              <Button variant="danger" size="sm" onClick={() => deleteGroup(g)}><Trash2 size={13} /></Button>
            </div>
          ))}
          {!groups.length && <EmptyState title="Sin grupos" message="Crea un grupo y asígnale dispositivos." icon={<UserRound size={24} />} />}
        </div>
      )}

      {tab === 'types' && isAdmin && (
        <div className="rules-list">
          <div className="table-toolbar">
            <span className="muted small">Tipos de equipo disponibles al crear o editar dispositivos.</span>
            <Button variant="primary" size="sm" onClick={() => openTypeModal()}><Plus size={14} /> Nuevo tipo</Button>
          </div>
          {deviceTypes.map(t => (
            <div className="rule-row" key={t.id}>
              <span className="map-tree-status" style={{ background: t.color || '#2563eb', width: 12, height: 12, borderRadius: 99 }} />
              <div className="rule-main"><b>{t.label}</b><span>{t.name} · forma {t.shape} · {t.probe_ids?.length ? `${t.probe_ids.length} sondas por defecto` : 'sin sondas por defecto'}{t.description ? ` · ${t.description}` : ''}</span></div>
              {t.is_builtin && <span className="builtin-pill">built-in</span>}
              <Button variant="secondary" size="sm" onClick={() => openTypeModal(t)}><Pencil size={13} /> Editar</Button>
              {!t.is_builtin && <Button variant="danger" size="sm" onClick={() => deleteType(t)}><Trash2 size={13} /></Button>}
            </div>
          ))}
        </div>
      )}

      {tab === 'users' && isAdmin && (
        <div className="rules-list">
          <div className="table-toolbar">
            <span className="muted small">Los usuarios administradores pueden cambiar configuración global; los operadores gestionan mapas y alertas; los viewers solo consultan.</span>
            <Button variant="primary" size="sm" onClick={() => { setError(''); setUserOpen(true) }}><Plus size={14} /> Nuevo usuario</Button>
          </div>
          {users.map(u => (
            <div className="rule-row" key={u.id}>
              <div className="avatar" style={{ width: 30, height: 30 }}>{(u.display_name || u.username).slice(0, 1).toUpperCase()}</div>
              <div className="rule-main"><b>{u.display_name || u.username}</b><span>@{u.username} · {u.role}{u.last_login_at ? ` · último acceso ${new Date(u.last_login_at).toLocaleDateString('es-ES')}` : ''}</span></div>
              <span className="builtin-pill">{u.role}{u.restrict_maps ? ' · mapas restringidos' : ''}{u.restrict_groups ? ' · grupos restringidos' : ''}</span>
              {u.id !== sessionUser?.id && <Button variant="secondary" size="sm" onClick={() => openUserMaps(u)}><KeyRound size={13} /> Mapas</Button>}
              {u.id !== sessionUser?.id && <Button variant="secondary" size="sm" onClick={() => openUserGroups(u)}><UserRound size={13} /> Grupos</Button>}
              {u.id !== sessionUser?.id && <Button variant="secondary" size="sm" onClick={() => openUserDevices(u)}><Server size={13} /> Disp.</Button>}
              {u.id !== sessionUser?.id && <Button variant="secondary" size="sm" onClick={() => openProbeTypes(u)}><Activity size={13} /> Sondas</Button>}
              {u.id !== sessionUser?.id && <Button variant="danger" size="sm" onClick={() => deleteUser(u.id)}><Trash2 size={13} /></Button>}
            </div>
          ))}
          {!users.length && <EmptyState title="Sin usuarios" message="Crea usuarios para separar operadores y viewers." icon={<UserRound size={24} />} />}
        </div>
      )}

      {tab === 'agents' && isAdmin && (
        <div className="rules-list">
          <div className="table-toolbar">
            <span className="muted small">Agentes de sondeo remoto. Se conectan saliendo hacia el servidor, sin abrir puertos en la sede remota.</span>
            <Button variant="secondary" size="sm" onClick={publishAgentRelease}>Publicar release</Button>
            <Button variant="secondary" size="sm" onClick={batchDeployAgents}>Desplegar canal</Button>
            <Button variant="primary" size="sm" onClick={() => { setError(''); setAgentToken(''); setAgentOpen(true) }}><Plus size={14} /> Nuevo agente</Button>
          </div>
          {agents.map(a => (
            <div className="rule-row" key={a.id}>
              <div className="settings-icon"><Server size={17} /></div>
              <div className="rule-main">
                <b>{a.name}</b>
                <span>{a.device_count}/{a.max_devices || '∞'} dispositivos · {a.version ? `v${a.version} · ` : ''}{a.last_seen_at ? `visto ${new Date(a.last_seen_at).toLocaleString('es-ES')}` : 'nunca conectado'}</span>
              </div>
              <StatusBadge status={a.online ? 'up' : 'unknown'} text={a.online ? 'Online' : 'Offline'} />
              <Button variant="secondary" size="sm" onClick={() => setAgentQuota(a)}>Cuota</Button>
              <Button variant="secondary" size="sm" onClick={() => updateAgent(a)}>Actualizar</Button>
              <Button variant="danger" size="sm" onClick={() => deleteAgent(a.id)}><Trash2 size={13} /></Button>
            </div>
          ))}
          {!agents.length && <EmptyState title="Sin agentes" message="Crea un agente para monitorizar sedes remotas tras NAT o VPN." icon={<Server size={24} />} />}
        </div>
      )}

      {tab === 'mibs' && (
        <div className="rules-list">
          <div className="table-toolbar">
            <span className="muted small">Catálogo MIB para traducir OIDs en SNMP Walk. Si el MIB importa otros módulos (IMPORTS), selecciónalos también en el mismo lote.</span>
            {isEditor && <Button variant="primary" size="sm" onClick={() => mibRef.current?.click()} disabled={mibImporting}><BookOpen size={14} /> {mibImporting ? 'Importando…' : 'Importar MIB'}</Button>}
            <input ref={mibRef} type="file" multiple accept=".mib,.txt,.my,text/plain" hidden onChange={importMIB} />
          </div>
          {mibError && <div className="form-error"><AlertTriangle size={14} /> {mibError}</div>}
          {mibResult && <div className="mib-result"><Check size={14} /> {mibResult}</div>}
          {mibs.map(m => (
            <div className="rule-row" key={m.module}>
              <div className="settings-icon"><BookOpen size={17} /></div>
              <div className="rule-main"><b>{m.module}</b><span>{m.objects} objetos · {m.imported_at ? new Date(m.imported_at).toLocaleString('es-ES') : ''}</span></div>
              {isEditor && <Button variant="danger" size="sm" onClick={() => deleteMIB(m.module)}><Trash2 size={13} /></Button>}
            </div>
          ))}
          {!mibs.length && <EmptyState title="Sin MIBs" message="Importa una MIB para ver nombres de OID en el SNMP Walk y en el catálogo." icon={<BookOpen size={24} />} />}
        </div>
      )}

      {tab === 'audit' && isAdmin && (
        <div className="rules-list">
          <div className="table-toolbar"><span className="muted small">Últimas 500 operaciones de escritura. No se almacenan cuerpos de petición ni secretos.</span></div>
          {auditLogs.map(a => (
            <div className="rule-row audit-row" key={a.id}>
              <span className="mono audit-time">{new Date(a.at).toLocaleString('es-ES')}</span>
              <span className="audit-user">{a.username || 'sistema'}</span>
              <span className={`audit-method method-${a.method}`}>{a.method}</span>
              <span className="mono audit-path">{a.path}</span>
              <span className={`audit-status ${a.status < 300 ? 'ok' : 'danger'}`}>{a.status}</span>
              <span className="mono muted">{a.ip}</span>
            </div>
          ))}
          {!auditLogs.length && <EmptyState title="Sin actividad" message="Las operaciones de escritura aparecerán aquí." icon={<ScrollText size={24} />} />}
        </div>
      )}

      {tab === 'maintenance' && isEditor && (
        <div className="rules-list">
          <div className="table-toolbar">
            <span className="muted small">Festivos globales (YYYY-MM-DD separados por comas). Suprimen avisos en todas las reglas.</span>
            <input className="input mono" style={{ maxWidth: 360 }} value={globalHolidays} onChange={e => setGlobalHolidays(e.target.value)} placeholder="2026-12-25, 2027-01-01" />
            <Button variant="secondary" size="sm" onClick={saveGlobalHolidays}>Guardar festivos</Button>
          </div>
          <div className="table-toolbar">
            <span className="muted small">Ventanas por dispositivo, mapa o etiqueta. Durante la ventana se suprimen los avisos externos.</span>
            <Button variant="primary" size="sm" onClick={openMaintenanceModal}><Plus size={14} /> Nueva ventana</Button>
          </div>
          {maintenanceWindows.map(w => {
            const start = new Date(w.starts_at).getTime()
            const end = new Date(w.ends_at).getTime()
            const now = Date.now()
            const state = now >= start && now < end ? 'Activa' : now < start ? 'Programada' : 'Finalizada'
            return (
              <div className="rule-row" key={w.id}>
                <div className="settings-icon"><Pause size={17} /></div>
                <div className="rule-main">
                  <b>{w.name}</b>
                  <span>{w.scope_type}{w.scope_name ? ` · ${w.scope_name}` : w.scope_tag ? ` · ${w.scope_tag}` : ''} · {new Date(w.starts_at).toLocaleString('es-ES')} → {new Date(w.ends_at).toLocaleString('es-ES')}</span>
                </div>
                <StatusBadge status={state === 'Activa' ? 'disabled' : state === 'Programada' ? 'warning' : 'unknown'} text={state} />
                <div className="row-actions">
                  <Button variant="danger" size="sm" aria-label="Eliminar ventana" onClick={() => deleteMaintenanceWindow(w)}><Trash2 size={13} /></Button>
                </div>
              </div>
            )
          })}
          {!maintenanceWindows.length && <EmptyState title="Sin ventanas" message="Programa mantenimientos para silenciar avisos por dispositivo, mapa o etiqueta." icon={<Pause size={24} />} />}
        </div>
      )}

      {tab === 'api_keys' && isAdmin && (
        <div className="rules-list">
          <div className="table-toolbar">
            <span className="muted small">Claves para integraciones vía cabecera <code>X-API-Key</code>. El token solo se muestra al crearla.</span>
            <Button variant="primary" size="sm" onClick={() => { setError(''); setApiKeyName(''); setApiKeyRole('viewer'); setApiKeyToken(''); setApiKeyOpen(true) }}><Plus size={14} /> Nueva API key</Button>
          </div>
          {apiKeys.map(k => (
            <div className="rule-row" key={k.id}>
              <div className="settings-icon"><KeyRound size={17} /></div>
              <div className="rule-main">
                <b>{k.name}</b>
                <span>{k.role} · {k.last_used_at ? `usada ${new Date(k.last_used_at).toLocaleString('es-ES')}` : 'nunca usada'} · creada {new Date(k.created_at).toLocaleDateString('es-ES')}</span>
              </div>
              <StatusBadge status={k.is_enabled ? 'up' : 'disabled'} text={k.is_enabled ? 'Activa' : 'Pausada'} />
              <div className="row-actions">
                <Button variant="secondary" size="sm" aria-label={k.is_enabled ? 'Pausar API key' : 'Activar API key'} onClick={() => toggleAPIKey(k)}>{k.is_enabled ? <Pause size={13} /> : <Play size={13} />}</Button>
                <Button variant="danger" size="sm" aria-label="Eliminar API key" onClick={() => deleteAPIKey(k)}><Trash2 size={13} /></Button>
              </div>
            </div>
          ))}
          {!apiKeys.length && <EmptyState title="Sin API keys" message="Crea una clave para integraciones y pantallas NOC." icon={<KeyRound size={24} />} />}
        </div>
      )}

      {tab === 'rules' && (
        <div className="rules-list">
          {rules.map(r => (
            <div className="rule-row" key={r.id}>
              <div className="settings-icon"><BellRing size={17} /></div>
              <div className="rule-main"><b>{r.name}</b><span>Prioridad {r.priority} · {String(r.trigger_transitions).replace(/[{}]/g, '')} · {scheduleLabel(r.active_schedule)}{r.group_by === 'map' ? ` · Agrupado por mapa ${r.group_window_sec}s` : r.group_by === 'tag' ? ` · Agrupado por etiqueta ${r.group_window_sec}s` : r.group_by === 'submap' ? ` · Agrupado por mapa padre ${r.group_window_sec}s` : r.group_by === 'zone' ? ` · Agrupado por zona ${r.group_window_sec}s` : ''}</span></div>
              <StatusBadge status={r.is_enabled ? 'up' : 'disabled'} text={r.is_enabled ? 'Activa' : 'Pausada'} />
              <div className="row-actions">
                <Button variant="secondary" size="sm" aria-label={r.is_enabled ? 'Pausar regla' : 'Activar regla'} onClick={() => toggleRule(r)}>{r.is_enabled ? <Pause size={13} /> : <Play size={13} />}</Button>
                <Button variant="secondary" size="sm" aria-label="Editar regla" onClick={() => openRuleModal(r)}><Pencil size={13} /></Button>
                <Button variant="danger" size="sm" aria-label="Eliminar regla" onClick={() => deleteRule(r)}><Trash2 size={13} /></Button>
              </div>
            </div>
          ))}
          {!rules.length && <EmptyState title="Sin reglas" message="Crea una regla para decidir cuándo y por dónde se avisa." icon={<SlidersHorizontal size={24} />} />}
        </div>
      )}

      {tab === 'rules' && (
        <div className="rules-list" style={{ marginTop: 14 }}>
          <div className="table-toolbar">
            <span className="muted small">Resúmenes periódicos de incidencias activas enviados por los canales elegidos.</span>
            <Button variant="secondary" size="sm" onClick={() => openDigestModal()}><Plus size={14} /> Nuevo resumen</Button>
          </div>
          {digestRules.map(d => (
            <div className="rule-row" key={d.id}>
              <div className="settings-icon"><Mail size={17} /></div>
              <div className="rule-main"><b>{d.name}</b><span>Cada {Math.round(d.interval_sec / 60)} min · {d.last_sent_at ? `último ${new Date(d.last_sent_at).toLocaleString('es-ES')}` : 'pendiente'}</span></div>
              <StatusBadge status={d.is_enabled ? 'up' : 'disabled'} text={d.is_enabled ? 'Activo' : 'Pausado'} />
              <div className="row-actions">
                <Button variant="secondary" size="sm" aria-label={d.is_enabled ? 'Pausar resumen' : 'Activar resumen'} onClick={() => toggleDigest(d)}>{d.is_enabled ? <Pause size={13} /> : <Play size={13} />}</Button>
                <Button variant="secondary" size="sm" aria-label="Editar resumen" onClick={() => openDigestModal(d)}><Pencil size={13} /></Button>
                <Button variant="danger" size="sm" aria-label="Eliminar resumen" onClick={() => deleteDigest(d)}><Trash2 size={13} /></Button>
              </div>
            </div>
          ))}
          {!digestRules.length && <EmptyState title="Sin resúmenes" message="Programa un resumen periódico de incidencias." icon={<Mail size={24} />} />}
        </div>
      )}

      <Modal open={channelOpen} onClose={() => { setChannelOpen(false); setChannelEditing(null) }} title={channelEditing ? 'Editar canal de aviso' : 'Nuevo canal de aviso'} subtitle="Telegram, Email, Webhook, Slack, Discord, Teams, ntfy, Syslog, PagerDuty, Opsgenie o SMS." width={620} footer={<><Button variant="ghost" onClick={() => { setChannelOpen(false); setChannelEditing(null) }}>Cancelar</Button><Button variant="primary" onClick={saveChannel} disabled={saving}>{saving ? 'Guardando…' : channelEditing ? 'Guardar cambios' : 'Crear canal'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={channelForm.name} onChange={e => setChannelForm({ ...channelForm, name: e.target.value })} placeholder="Guardia Telegram" /></Field>
          <Field label="Tipo"><select className="input" value={channelForm.type} onChange={e => {
            const type = e.target.value
            setChannelForm({ ...channelForm, type })
            setChannelConfig(defaultChannelConfig(type))
            setError('')
          }}><option value="telegram">Telegram</option><option value="email">Email SMTP</option><option value="webhook">Webhook</option><option value="slack">Slack</option><option value="discord">Discord</option><option value="teams">Microsoft Teams</option><option value="ntfy">ntfy.sh</option><option value="web_audio">Navegador NOC</option><option value="agent_action">Acción en agente</option><option value="syslog">Syslog</option><option value="pagerduty">PagerDuty</option><option value="opsgenie">Opsgenie</option><option value="sms">SMS Twilio</option></select></Field>
        </div>
        {channelForm.type === 'telegram' ? (
          <div className="form-grid two">
            <Field label="Bot token" hint="Token de @BotFather."><input className="input mono" value={telegramToken} onChange={e => setTelegramToken(e.target.value)} placeholder="123456789:AA…" /></Field>
            <Field label="Chat ID" hint="ID del chat, grupo o canal."><input className="input mono" value={telegramChat} onChange={e => setTelegramChat(e.target.value)} placeholder="541173076" /></Field>
          </div>
        ) : (CHANNEL_FIELDS[channelForm.type] || []).length > 0 ? (
          <div className="form-grid two">
            {(CHANNEL_FIELDS[channelForm.type] || []).map(field => (
              <Field key={field.key} label={field.label} hint={field.hint}>
                {field.type === 'agent' ? (
                  <select className="input" value={channelConfig[field.key] || ''} onChange={e => setChannelConfig({ ...channelConfig, [field.key]: e.target.value })}>
                    <option value="">— Selecciona agente</option>
                    {agents.map(agent => <option key={agent.id} value={agent.id}>{agent.name}{agent.online ? ' · online' : ' · offline'}</option>)}
                  </select>
                ) : field.options ? (
                  <select className="input" value={channelConfig[field.key] || field.options[0]} onChange={e => setChannelConfig({ ...channelConfig, [field.key]: e.target.value })}>
                    {field.options.map(option => <option key={option} value={option}>{option}</option>)}
                  </select>
                ) : field.type === 'textarea' ? (
                  <textarea className="input textarea" rows={3} value={channelConfig[field.key] || ''} onChange={e => setChannelConfig({ ...channelConfig, [field.key]: e.target.value })} placeholder={field.placeholder} />
                ) : (
                  <input className="input" type={field.type || 'text'} value={channelConfig[field.key] || ''} onChange={e => setChannelConfig({ ...channelConfig, [field.key]: e.target.value })} placeholder={field.placeholder} />
                )}
              </Field>
            ))}
          </div>
        ) : (
          <div className="muted small">Este tipo de canal no necesita configuración adicional.</div>
        )}
        <label className="check-label"><input type="checkbox" checked={channelForm.enabled} onChange={e => setChannelForm({ ...channelForm, enabled: e.target.checked })} /> Canal activo</label>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={apiKeyOpen} onClose={() => { setApiKeyOpen(false); setApiKeyToken('') }} title="Nueva API key" subtitle="Para integraciones, scripts y pantallas NOC sin sesión de usuario." width={560} footer={<><Button variant="ghost" onClick={() => { setApiKeyOpen(false); setApiKeyToken('') }}>{apiKeyToken ? 'Cerrar' : 'Cancelar'}</Button>{!apiKeyToken && <Button variant="primary" onClick={createAPIKey} disabled={saving || !apiKeyName.trim()}>{saving ? 'Creando…' : 'Crear API key'}</Button>}</>}>
        {!apiKeyToken ? (
          <div className="form-grid two">
            <Field label="Nombre" hint="Ej. Grafana, Zabbix, pantalla NOC."><input className="input" value={apiKeyName} onChange={e => setApiKeyName(e.target.value)} placeholder="Integración NOC" autoFocus /></Field>
            <Field label="Rol"><select className="input" value={apiKeyRole} onChange={e => setApiKeyRole(e.target.value)}><option value="viewer">Viewer (solo lectura)</option><option value="operator">Operador</option><option value="admin">Administrador</option></select></Field>
          </div>
        ) : (
          <div className="agent-token-box">
            <b>API key creada</b>
            <p>Cópiala ahora. No volverá a mostrarse.</p>
            <div className="agent-token-row"><code>{apiKeyToken}</code><IconButton label="Copiar API key" onClick={() => navigator.clipboard?.writeText(apiKeyToken)}><Copy size={15} /></IconButton></div>
            <p className="field-hint">Uso: cabecera <code>X-API-Key: {apiKeyToken.slice(0, 12)}…</code></p>
          </div>
        )}
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={profileOpen} onClose={() => { setProfileOpen(false); setProfileEditing(null) }} title={profileEditing ? 'Editar perfil SNMP' : 'Nuevo perfil SNMP'} subtitle="Credenciales SNMP v1, v2c o v3. Deja las contraseñas vacías para conservarlas." width={620} footer={<><Button variant="ghost" onClick={() => { setProfileOpen(false); setProfileEditing(null) }}>Cancelar</Button><Button variant="primary" onClick={saveProfile} disabled={saving}>{saving ? 'Guardando…' : profileEditing ? 'Guardar cambios' : 'Crear perfil'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={profileForm.name} onChange={e => setProfileForm({ ...profileForm, name: e.target.value })} placeholder="Cisco IOS v2c" /></Field>
          <Field label="Versión"><select className="input" value={profileForm.version} onChange={e => setProfileForm({ ...profileForm, version: e.target.value })}><option value="v1">SNMP v1</option><option value="v2c">SNMP v2c</option><option value="v3">SNMP v3</option></select></Field>
          <Field label="Puerto"><input className="input" type="number" value={profileForm.port} onChange={e => setProfileForm({ ...profileForm, port: Number(e.target.value) })} /></Field>
          {profileForm.version !== 'v3' ? (
            <Field label="Community"><input className="input mono" value={profileForm.community} onChange={e => setProfileForm({ ...profileForm, community: e.target.value })} /></Field>
          ) : (
            <Field label="Usuario v3"><input className="input" value={profileForm.user} onChange={e => setProfileForm({ ...profileForm, user: e.target.value })} /></Field>
          )}
        </div>
        {profileForm.version === 'v3' && (
          <div className="form-grid two">
            <Field label="Auth"><select className="input" value={profileForm.auth} onChange={e => setProfileForm({ ...profileForm, auth: e.target.value })}><option>SHA</option><option>MD5</option><option>SHA256</option><option>SHA512</option></select></Field>
            <Field label="Auth password"><input className="input" type="password" value={profileForm.authPass} onChange={e => setProfileForm({ ...profileForm, authPass: e.target.value })} /></Field>
            <Field label="Priv"><select className="input" value={profileForm.priv} onChange={e => setProfileForm({ ...profileForm, priv: e.target.value })}><option>AES</option><option>DES</option><option>AES256</option></select></Field>
            <Field label="Priv password"><input className="input" type="password" value={profileForm.privPass} onChange={e => setProfileForm({ ...profileForm, privPass: e.target.value })} /></Field>
          </div>
        )}
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={agentOpen} onClose={() => { setAgentOpen(false); setAgentName(''); setAgentToken('') }} title="Nuevo agente de sondeo" subtitle="El token solo se muestra una vez. Guárdalo antes de cerrar." footer={<><Button variant="ghost" onClick={() => { setAgentOpen(false); setAgentName(''); setAgentToken('') }}>Cerrar</Button>{!agentToken && <Button variant="primary" onClick={createAgent} disabled={saving || !agentName.trim()}>{saving ? 'Creando…' : 'Crear agente'}</Button>}</>}>
        {!agentToken ? (
          <div className="form-grid two">
            <Field label="Nombre del agente" hint="Ej. Sede-Norte, CPD-Sevilla, Cliente-ACME.">
              <input className="input" value={agentName} onChange={e => setAgentName(e.target.value)} placeholder="Agente sede remota" autoFocus />
            </Field>
            <Field label="Cuota de dispositivos" hint="0 = sin límite.">
              <input className="input" type="number" min={0} value={agentMax} onChange={e => setAgentMax(Number(e.target.value))} />
            </Field>
          </div>
        ) : (
          <div className="agent-token-box">
            <b>Token de conexión</b>
            <p>Copia este token en el contenedor remoto. No volverá a mostrarse.</p>
            <div className="agent-token-row">
              <code>{agentToken}</code>
              <IconButton label="Copiar token" onClick={() => navigator.clipboard?.writeText(agentToken)}><Copy size={15} /></IconButton>
            </div>
            <p className="field-hint">Guía rápida:</p>
            <pre>{`AGENT_CORE_WS_URL=wss://tu-servidor/api/v1/agent/ws\nAGENT_TOKEN=${agentToken}\ndocker compose -f docker-compose.yml -f docker-compose.advanced.yml --profile agent up -d`}</pre>
          </div>
        )}
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={userOpen} onClose={() => setUserOpen(false)} title="Nuevo usuario" subtitle="Mínimo 8 caracteres de contraseña." footer={<><Button variant="ghost" onClick={() => setUserOpen(false)}>Cancelar</Button><Button variant="primary" onClick={createUser} disabled={saving}>{saving ? 'Creando…' : 'Crear usuario'}</Button></>}>
        <div className="form-grid two">
          <Field label="Usuario"><input className="input mono" value={userForm.username} onChange={e => setUserForm({ ...userForm, username: e.target.value })} autoFocus /></Field>
          <Field label="Nombre visible"><input className="input" value={userForm.display_name} onChange={e => setUserForm({ ...userForm, display_name: e.target.value })} /></Field>
          <Field label="Contraseña"><input className="input" type="password" value={userForm.password} onChange={e => setUserForm({ ...userForm, password: e.target.value })} /></Field>
          <Field label="Rol"><select className="input" value={userForm.role} onChange={e => setUserForm({ ...userForm, role: e.target.value })}><option value="operator">Operador</option><option value="viewer">Viewer</option><option value="admin">Administrador</option></select></Field>
        </div>
        <label className="check-label"><input type="checkbox" checked={userForm.restrict_maps} onChange={e => setUserForm({ ...userForm, restrict_maps: e.target.checked })} /> Restringir este usuario a mapas concretos</label>
        <label className="check-label"><input type="checkbox" checked={userForm.restrict_groups} onChange={e => setUserForm({ ...userForm, restrict_groups: e.target.checked })} /> Restringir a grupos de dispositivos</label>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={accessOpen} onClose={() => setAccessOpen(false)} title={`Acceso a mapas · ${accessUser?.username || ''}`} subtitle="Si se guarda, el usuario solo verá estos mapas, sus submapas y sus dispositivos." footer={<><Button variant="ghost" onClick={() => setAccessOpen(false)}>Cancelar</Button><Button variant="primary" onClick={saveUserMaps} disabled={accessSaving}>{accessSaving ? 'Guardando…' : 'Guardar accesos'}</Button></>}>
        <div className="parent-picker">
          {allMaps.map(m => (
            <label key={m.id} className={`parent-option ${accessMaps.includes(m.id) ? 'active' : ''}`}>
              <input type="checkbox" checked={accessMaps.includes(m.id)} onChange={e => setAccessMaps(e.target.checked ? [...accessMaps, m.id] : accessMaps.filter(id => id !== m.id))} />
              <span className={`map-tree-status status-${m.status}`} />
              <span>{m.name}</span>
              <small>{m.counts.up + m.counts.warning + m.counts.down + m.counts.unreachable} dispositivos</small>
            </label>
          ))}
          {!allMaps.length && <span className="muted small">No hay mapas.</span>}
        </div>
      </Modal>

      <Modal open={typeOpen} onClose={() => { setTypeOpen(false); setTypeEditing(null) }} title={typeEditing ? 'Editar tipo de equipo' : 'Nuevo tipo de equipo'} subtitle="Nombre interno, etiqueta visible, forma y color." footer={<><Button variant="ghost" onClick={() => { setTypeOpen(false); setTypeEditing(null) }}>Cancelar</Button><Button variant="primary" onClick={saveType} disabled={saving}>{saving ? 'Guardando…' : typeEditing ? 'Guardar cambios' : 'Crear tipo'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre interno" hint="Sin espacios; se usa en la API."><input className="input mono" value={typeForm.name} disabled={!!typeEditing} onChange={e => setTypeForm({ ...typeForm, name: e.target.value })} placeholder="olt" /></Field>
          <Field label="Etiqueta"><input className="input" value={typeForm.label} onChange={e => setTypeForm({ ...typeForm, label: e.target.value })} placeholder="OLT" /></Field>
          <Field label="Icono" hint="Cisco/Lucide (router, switch, wifi…) o custom:mi-icono.svg"><input className="input" value={typeForm.icon} onChange={e => setTypeForm({ ...typeForm, icon: e.target.value })} placeholder="router, wifi, custom:mi-icono.svg" /></Field>
          <Field label="Forma"><select className="input" value={typeForm.shape} onChange={e => setTypeForm({ ...typeForm, shape: e.target.value })}><option value="rounded_rect">Rectángulo</option><option value="square">Cuadrado</option><option value="circle">Círculo</option><option value="pill">Cápsula</option><option value="diamond">Rombo</option><option value="hexagon">Hexágono</option></select></Field>
          <Field label="Color"><input className="input" type="color" value={typeForm.color} onChange={e => setTypeForm({ ...typeForm, color: e.target.value })} /></Field>
        </div>
        <Field label="Descripción"><input className="input" value={typeForm.description} onChange={e => setTypeForm({ ...typeForm, description: e.target.value })} /></Field>
        <Field label={`Sondas por defecto (${typeForm.probe_ids.length})`} hint="Se crean automáticamente al añadir un dispositivo de este tipo. La sonda Ping se sigue creando si no está incluida.">
          <div className="parent-picker" style={{ maxHeight: 220, overflowY: 'auto' }}>
            {probes.map(p => (
              <label key={p.id} className={`parent-option ${typeForm.probe_ids.includes(p.id) ? 'active' : ''}`}>
                <input type="checkbox" checked={typeForm.probe_ids.includes(p.id)}
                  onChange={e => setTypeForm({ ...typeForm, probe_ids: e.target.checked ? [...typeForm.probe_ids, p.id] : typeForm.probe_ids.filter(id => id !== p.id) })} />
                <span>{p.name}</span>
                <small>{p.probe_type}</small>
              </label>
            ))}
            {!probes.length && <span className="muted small">No hay sondas disponibles.</span>}
          </div>
        </Field>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={groupOpen} onClose={() => { setGroupOpen(false); setGroupEditing(null) }} title={groupEditing ? 'Editar grupo' : 'Nuevo grupo de dispositivos'} subtitle="Selecciona los dispositivos que pertenecen al grupo." width={680} footer={<><Button variant="ghost" onClick={() => { setGroupOpen(false); setGroupEditing(null) }}>Cancelar</Button><Button variant="primary" onClick={saveGroup} disabled={saving}>{saving ? 'Guardando…' : groupEditing ? 'Guardar cambios' : 'Crear grupo'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={groupForm.name} onChange={e => setGroupForm({ ...groupForm, name: e.target.value })} placeholder="CPD Central" autoFocus /></Field>
          <Field label="Color"><input className="input" type="color" value={groupForm.color} onChange={e => setGroupForm({ ...groupForm, color: e.target.value })} /></Field>
        </div>
        <Field label="Descripción"><input className="input" value={groupForm.description} onChange={e => setGroupForm({ ...groupForm, description: e.target.value })} /></Field>
        <div className="form-grid two">
          <Field label="Polling del grupo (s)" hint="0 = heredar. Prevalece sobre el mapa."><input className="input" type="number" min={0} value={groupForm.poll_interval_sec} onChange={e => setGroupForm({ ...groupForm, poll_interval_sec: Number(e.target.value) })} /></Field>
          <Field label="Reintentos del grupo"><input className="input" type="number" min={0} value={groupForm.poll_down_retries} onChange={e => setGroupForm({ ...groupForm, poll_down_retries: Number(e.target.value) })} /></Field>
          <Field label="Agente del grupo"><select className="input" value={groupForm.poll_agent_id} onChange={e => setGroupForm({ ...groupForm, poll_agent_id: e.target.value })}><option value="">— Heredar</option>{agents.map(a => <option key={a.id} value={a.id}>{a.name}{a.online ? ' · online' : ' · offline'}</option>)}</select></Field>
          <Field label="Notificaciones"><select className="input" value={groupForm.poll_notifications} onChange={e => setGroupForm({ ...groupForm, poll_notifications: Number(e.target.value) })}><option value={-1}>Heredar</option><option value={1}>Activadas</option><option value={0}>Silenciadas</option></select></Field>
        </div>
        <Field label={`Dispositivos (${groupForm.deviceIDs.length})`}>
          <div className="parent-picker" style={{ maxHeight: 260, overflowY: 'auto' }}>
            {groupDevices.map(d => (
              <label key={d.id} className={`parent-option ${groupForm.deviceIDs.includes(d.id) ? 'active' : ''}`}>
                <input type="checkbox" checked={groupForm.deviceIDs.includes(d.id)}
                  onChange={e => setGroupForm({ ...groupForm, deviceIDs: e.target.checked ? [...groupForm.deviceIDs, d.id] : groupForm.deviceIDs.filter(id => id !== d.id) })} />
                <span className={`map-tree-status status-${d.status}`} />
                <span>{d.name}</span>
                <small>{d.primary_ip}</small>
              </label>
            ))}
          </div>
        </Field>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={groupAccessOpen} onClose={() => setGroupAccessOpen(false)} title={`Grupos de dispositivos · ${groupAccessUser?.username || ''}`} subtitle="El usuario solo verá los dispositivos incluidos en estos grupos. Se combina con la restricción por mapas." footer={<><Button variant="ghost" onClick={() => setGroupAccessOpen(false)}>Cancelar</Button><Button variant="primary" onClick={saveUserGroups} disabled={groupAccessSaving}>{groupAccessSaving ? 'Guardando…' : 'Guardar grupos'}</Button></>}>
        <div className="parent-picker">
          {groups.map(g => (
            <label key={g.id} className={`parent-option ${userGroupIDs.includes(g.id) ? 'active' : ''}`}>
              <input type="checkbox" checked={userGroupIDs.includes(g.id)}
                onChange={e => setUserGroupIDs(e.target.checked ? [...userGroupIDs, g.id] : userGroupIDs.filter(id => id !== g.id))} />
              <span className="map-tree-status" style={{ background: g.color || '#2563eb' }} />
              <span>{g.name}</span>
              <small>{g.device_count} dispositivos</small>
            </label>
          ))}
          {!groups.length && <span className="muted small">No hay grupos. Crea uno en la pestaña Grupos.</span>}
        </div>
      </Modal>

      <Modal open={userDevicesOpen} onClose={() => setUserDevicesOpen(false)} title={`Dispositivos · ${userDevicesUser?.username || ''}`} subtitle="El usuario solo verá estos dispositivos, además de los incluidos en sus grupos." footer={<><Button variant="ghost" onClick={() => setUserDevicesOpen(false)}>Cancelar</Button><Button variant="primary" onClick={saveUserDevices}>Guardar dispositivos</Button></>}>
        <div className="parent-picker" style={{ maxHeight: 300, overflowY: 'auto' }}>
          {userDeviceList.map(d => (
            <label key={d.id} className={`parent-option ${userDeviceIDs.includes(d.id) ? 'active' : ''}`}>
              <input type="checkbox" checked={userDeviceIDs.includes(d.id)}
                onChange={e => setUserDeviceIDs(e.target.checked ? [...userDeviceIDs, d.id] : userDeviceIDs.filter(id => id !== d.id))} />
              <span className={`map-tree-status status-${d.status}`} />
              <span>{d.name}</span>
              <small>{d.primary_ip}</small>
            </label>
          ))}
        </div>
      </Modal>

      <Modal open={probeTypesOpen} onClose={() => setProbeTypesOpen(false)} title={`Tipos de sonda · ${probeTypesUser?.username || ''}`} subtitle="Sin selección = todos los tipos permitidos." footer={<><Button variant="ghost" onClick={() => setProbeTypesOpen(false)}>Cancelar</Button><Button variant="primary" onClick={saveProbeTypes} disabled={probeTypeSaving}>{probeTypeSaving ? 'Guardando…' : 'Guardar tipos'}</Button></>}>
        <div className="checkbox-row">
          {['icmp', 'snmp', 'tcp', 'http', 'dns', 'function', 'logic'].map(t => (
            <label key={t} className="check-label">
              <input type="checkbox" checked={userProbeTypes.includes(t)}
                onChange={e => setUserProbeTypes(e.target.checked ? [...userProbeTypes, t] : userProbeTypes.filter(x => x !== t))} />
              {t.toUpperCase()}
            </label>
          ))}
        </div>
      </Modal>

      <Modal open={mwOpen} onClose={() => setMwOpen(false)} title="Nueva ventana de mantenimiento" subtitle="Silencia avisos externos durante el intervalo indicado." footer={<><Button variant="ghost" onClick={() => setMwOpen(false)}>Cancelar</Button><Button variant="primary" onClick={createMaintenanceWindow} disabled={saving}>{saving ? 'Guardando…' : 'Crear ventana'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={mwForm.name} onChange={e => setMwForm({ ...mwForm, name: e.target.value })} placeholder="Mantenimiento CPD" autoFocus /></Field>
          <Field label="Ámbito"><select className="input" value={mwForm.scopeType} onChange={e => setMwForm({ ...mwForm, scopeType: e.target.value, scopeID: '' })}><option value="device">Dispositivo</option><option value="map">Mapa</option><option value="tag">Etiqueta</option><option value="address_list">Lista de direcciones</option></select></Field>
        </div>
        {mwForm.scopeType === 'tag' ? (
          <Field label="Etiqueta"><input className="input" value={mwForm.scopeTag} onChange={e => setMwForm({ ...mwForm, scopeTag: e.target.value })} placeholder="critico" /></Field>
        ) : (
          <Field label={mwForm.scopeType === 'map' ? 'Mapa' : mwForm.scopeType === 'address_list' ? 'Lista de direcciones' : 'Dispositivo'}>
            <select className="input" value={mwForm.scopeID} onChange={e => setMwForm({ ...mwForm, scopeID: e.target.value })}>
              <option value="">— Selecciona</option>
              {(mwForm.scopeType === 'map' ? mwMaps : mwForm.scopeType === 'address_list' ? mwLists : mwDevices).map((x: any) => <option key={x.id} value={x.id}>{x.name}</option>)}
            </select>
          </Field>
        )}
        <div className="form-grid two">
          <Field label="Inicio"><input className="input" type="datetime-local" value={mwForm.starts} onChange={e => setMwForm({ ...mwForm, starts: e.target.value })} /></Field>
          <Field label="Fin"><input className="input" type="datetime-local" value={mwForm.ends} onChange={e => setMwForm({ ...mwForm, ends: e.target.value })} /></Field>
        </div>
        <Field label="Motivo"><input className="input" value={mwForm.reason} onChange={e => setMwForm({ ...mwForm, reason: e.target.value })} placeholder="Actualización de firmware" /></Field>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={digestOpen} onClose={() => { setDigestOpen(false); setDigestEditing(null) }} title={digestEditing ? 'Editar resumen periódico' : 'Nuevo resumen periódico'} subtitle="Envía un resumen de incidencias activas cada N minutos." footer={<><Button variant="ghost" onClick={() => { setDigestOpen(false); setDigestEditing(null) }}>Cancelar</Button><Button variant="primary" onClick={saveDigest} disabled={saving}>{saving ? 'Guardando…' : digestEditing ? 'Guardar cambios' : 'Crear resumen'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre"><input className="input" value={digestForm.name} onChange={e => setDigestForm({ ...digestForm, name: e.target.value })} autoFocus /></Field>
          <Field label="Cada (minutos)"><input className="input" type="number" min={1} value={digestForm.interval} onChange={e => setDigestForm({ ...digestForm, interval: Number(e.target.value) })} /></Field>
        </div>
        <Field label="Canales">
          <div className="checkbox-row">
            {channels.map(c => (
              <label key={c.id} className="check-label">
                <input type="checkbox" checked={digestForm.channels.includes(c.id)}
                  onChange={e => setDigestForm({ ...digestForm, channels: e.target.checked ? [...digestForm.channels, c.id] : digestForm.channels.filter(id => id !== c.id) })} />
                {c.name}
              </label>
            ))}
            {!channels.length && <span className="muted small">Crea un canal primero.</span>}
          </div>
        </Field>
        <label className="check-label"><input type="checkbox" checked={digestForm.enabled} onChange={e => setDigestForm({ ...digestForm, enabled: e.target.checked })} /> Resumen activo</label>
        {error && <div className="form-error">{error}</div>}
      </Modal>

      <Modal open={ruleOpen} onClose={() => { setRuleOpen(false); setRuleEditing(null) }} title={ruleEditing ? 'Editar regla de aviso' : 'Nueva regla de aviso'} subtitle="Define qué transiciones se notifican y por qué canales." footer={<><Button variant="ghost" onClick={() => { setRuleOpen(false); setRuleEditing(null) }}>Cancelar</Button><Button variant="primary" onClick={saveRule} disabled={saving}>{saving ? 'Guardando…' : ruleEditing ? 'Guardar cambios' : 'Crear regla'}</Button></>}>
        <div className="form-grid two">
          <Field label="Nombre de la regla"><input className="input" value={ruleForm.name} onChange={e => setRuleForm({ ...ruleForm, name: e.target.value })} placeholder="Guardia crítica" /></Field>
          <Field label="Canales">
            <div className="checkbox-row">
              {channels.map(c => (
                <label key={c.id} className="check-label">
                  <input type="checkbox" checked={ruleForm.channels.includes(c.id)}
                    onChange={e => setRuleForm({ ...ruleForm, channels: e.target.checked ? [...ruleForm.channels, c.id] : ruleForm.channels.filter(id => id !== c.id) })} />
                  {c.name}
                </label>
              ))}
              {!channels.length && <span className="muted small">Crea un canal primero.</span>}
            </div>
          </Field>
        </div>
        <Field label="Transiciones">
          <div className="checkbox-row">
            {['down', 'warning', 'recovered', 'flapping'].map(t => (
              <label key={t} className="check-label"><input type="checkbox" checked={ruleForm.transitions.includes(t)} onChange={e => setRuleForm({ ...ruleForm, transitions: e.target.checked ? [...ruleForm.transitions, t] : ruleForm.transitions.filter(x => x !== t) })} />{t === 'down' ? 'Caída' : t === 'warning' ? 'Advertencia' : t === 'recovered' ? 'Recuperación' : 'Oscilación'}</label>
            ))}
          </div>
        </Field>
        <Field label="Horario de la regla">
          <label className="check-label"><input type="checkbox" checked={ruleForm.customSchedule} onChange={e => setRuleForm({ ...ruleForm, customSchedule: e.target.checked })} /> Limitar a días y horas concretas</label>
        </Field>
        {ruleForm.customSchedule && (
          <div className="form-grid two">
            <Field label="Días" hint="Sin selección = todos los días.">
              <div className="checkbox-row">
                {DAY_LABELS.map((label, i) => {
                  const day = i + 1
                  return (
                    <label key={day} className="check-label">
                      <input type="checkbox" checked={ruleForm.days.includes(day)}
                        onChange={e => setRuleForm({ ...ruleForm, days: e.target.checked ? [...ruleForm.days, day].sort((a, b) => a - b) : ruleForm.days.filter(d => d !== day) })} />
                      {label}
                    </label>
                  )
                })}
              </div>
            </Field>
            <Field label="Zona horaria" hint="Ej. Europe/Madrid. Vacío = zona del servidor."><input className="input" value={ruleForm.timezone} onChange={e => setRuleForm({ ...ruleForm, timezone: e.target.value })} /></Field>
            <Field label="Desde"><input className="input" type="time" value={ruleForm.start} onChange={e => setRuleForm({ ...ruleForm, start: e.target.value })} /></Field>
            <Field label="Hasta" hint="Si la hora final es menor, la franja cruza medianoche."><input className="input" type="time" value={ruleForm.end} onChange={e => setRuleForm({ ...ruleForm, end: e.target.value })} /></Field>
          </div>
        )}
        <Field label="Festivos" hint="Fechas YYYY-MM-DD separadas por comas. No se envían avisos esos días."><input className="input mono" value={ruleForm.holidays} onChange={e => setRuleForm({ ...ruleForm, holidays: e.target.value })} placeholder="2026-12-25, 2027-01-01" /></Field>
        <div className="form-grid two">
          <Field label="Agrupación" hint="Agrupa por mapa o por la primera etiqueta del equipo y desactiva las repeticiones.">
            <select className="input" value={ruleForm.groupBy} onChange={e => setRuleForm({ ...ruleForm, groupBy: e.target.value })}>
              <option value="none">Individual</option>
              <option value="map">Agrupar por mapa</option>
              <option value="tag">Agrupar por etiqueta</option>
              <option value="submap">Agrupar por mapa padre</option>
              <option value="zone">Agrupar por zona visual</option>
            </select>
          </Field>
          {ruleForm.groupBy !== 'none' && (
            <Field label="Ventana de agrupación (s)" hint="Tiempo para acumular equipos antes de enviar el resumen. Se suma al retardo inicial."><input className="input" type="number" min={5} max={3600} value={ruleForm.groupWindow} onChange={e => setRuleForm({ ...ruleForm, groupWindow: Number(e.target.value) })} /></Field>
          )}
        </div>
        <Field label="Filtrar por listas de direcciones" hint="Sin selección = todos. Solo se avisará si el equipo cae dentro de alguna lista marcada.">
          <div className="checkbox-row">
            {addressLists.map(l => (
              <label key={l.id} className="check-label">
                <input type="checkbox" checked={ruleForm.addressLists.includes(l.id)}
                  onChange={e => setRuleForm({ ...ruleForm, addressLists: e.target.checked ? [...ruleForm.addressLists, l.id] : ruleForm.addressLists.filter(id => id !== l.id) })} />
                {l.name} {l.kind === 'block' ? '· block' : ''}
              </label>
            ))}
            {!addressLists.length && <span className="muted small">Crea listas en Redes y listas.</span>}
          </div>
        </Field>
        <label className="check-label"><input type="checkbox" checked={ruleForm.enabled} onChange={e => setRuleForm({ ...ruleForm, enabled: e.target.checked })} /> Regla activa</label>
        <div className="form-grid two">
          <Field label="Retardo inicial (s)"><input className="input" type="number" min={0} value={ruleForm.delay_sec} onChange={e => setRuleForm({ ...ruleForm, delay_sec: Number(e.target.value) })} /></Field>
          <Field label="Repetir cada (s)" hint="0 = no repetir. Cada repetición se cancela si el equipo se recupera o se reconoce."><input className="input" type="number" min={0} value={ruleForm.repeat_interval_sec} onChange={e => setRuleForm({ ...ruleForm, repeat_interval_sec: Number(e.target.value) })} /></Field>
          <Field label="Máximo de repeticiones"><input className="input" type="number" min={0} value={ruleForm.max_repeats} onChange={e => setRuleForm({ ...ruleForm, max_repeats: Number(e.target.value) })} /></Field>
        </div>
        {error && <div className="form-error">{error}</div>}
      </Modal>
    </div>
  )
}
