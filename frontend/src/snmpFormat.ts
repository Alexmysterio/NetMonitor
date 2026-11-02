export function formatBytes(n: number): string {
  if (!n || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let value = n
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${value >= 100 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`
}

export function ifStatus(v: number): string {
  switch (v) {
    case 1: return 'up'
    case 2: return 'down'
    case 3: return 'testing'
    case 4: return 'unknown'
    case 5: return 'dormant'
    case 6: return 'notPresent'
    case 7: return 'lowerLayerDown'
    default: return '—'
  }
}

export function ifTypeLabel(v: number): string {
  switch (v) {
    case 6: return 'ethernet'
    case 24: return 'loopback'
    case 131: return 'tunnel'
    case 135: return 'l2vlan'
    case 136: return 'l3ipvlan'
    case 161: return 'ieee8023adLag'
    case 53: return 'propVirtual'
    default: return v ? `tipo ${v}` : '—'
  }
}

export function routeTypeLabel(v: number): string {
  switch (v) {
    case 1: return 'otra'
    case 2: return 'rechazo'
    case 3: return 'local'
    case 4: return 'remota'
    default: return '—'
  }
}
