import {
  BatteryCharging,
  Building2,
  Camera,
  Cloud,
  Cpu,
  HardDrive,
  Monitor,
  Network,
  Printer,
  Radio,
  Router,
  Server,
  Shield,
  Wifi,
  type LucideIcon,
} from 'lucide-react'
import ciscoRouter from '../assets/cisco/cisco-router.svg'
import ciscoSwitch from '../assets/cisco/cisco-switch.svg'
import ciscoFirewall from '../assets/cisco/cisco-firewall.svg'
import ciscoWireless from '../assets/cisco/cisco-wireless.svg'
import ciscoServer from '../assets/cisco/cisco-server.svg'
import ciscoCPE from '../assets/cisco/cisco-cpe.svg'

const ICONS: Record<string, LucideIcon> = {
  router: Router,
  network: Network,
  server: Server,
  wifi: Wifi,
  'radio-tower': Radio,
  building: Building2,
  printer: Printer,
  shield: Shield,
  'battery-charging': BatteryCharging,
  monitor: Monitor,
  camera: Camera,
  cloud: Cloud,
  cpu: Cpu,
  storage: HardDrive,
}

const CISCO_ICONS: Record<string, string> = {
  router: ciscoRouter,
  network: ciscoRouter,
  cpe: ciscoCPE,
  switch: ciscoSwitch,
  firewall: ciscoFirewall,
  shield: ciscoFirewall,
  access_point: ciscoWireless,
  wifi: ciscoWireless,
  radio: ciscoWireless,
  'radio-tower': ciscoWireless,
  server: ciscoServer,
  storage: ciscoServer,
  generic: ciscoCPE,
  box: ciscoCPE,
}

export function resolveIconSource(name: string): string | null {
  const value = (name || '').trim()
  if (!value) return null
  if (value.startsWith('data:') || /^https?:\/\//i.test(value)) return value
  if (value.startsWith('/custom-icons/')) return value
  if (value.startsWith('custom-icons/')) return '/' + value
  if (value.startsWith('custom:')) return '/custom-icons/' + value.slice(7).replace(/^\/+/, '')
  if (/\.(svg|png|jpe?g|webp|gif)$/i.test(value)) return '/custom-icons/' + value.replace(/^\/+/, '')
  return CISCO_ICONS[value] || null
}

export default function NodeIcon({ name, size = 44 }: { name: string; size?: number }) {
  const src = resolveIconSource(name)
  if (src) return <img className="node-icon-img" style={{ width: size, height: size }} src={src} alt="" />
  const Icon = ICONS[name] || Server
  return <Icon size={Math.round(size * 0.6)} strokeWidth={2} />
}
