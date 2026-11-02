const TOKEN_KEY = 'netmonitor.token'
const USER_KEY = 'netmonitor.user'

export interface SessionUser {
  id: string
  username: string
  display_name: string
  role: string
}

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function getUser(): SessionUser | null {
  const raw = localStorage.getItem(USER_KEY)
  if (!raw) return null
  try {
    return JSON.parse(raw) as SessionUser
  } catch {
    return null
  }
}

export function setSession(token: string, user: SessionUser) {
  localStorage.setItem(TOKEN_KEY, token)
  localStorage.setItem(USER_KEY, JSON.stringify(user))
  window.dispatchEvent(new Event('netmonitor:session'))
}

export function clearSession() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
  window.dispatchEvent(new Event('netmonitor:session'))
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  const isForm = typeof FormData !== 'undefined' && options.body instanceof FormData
  if (!headers.has('Content-Type') && options.body && !isForm) headers.set('Content-Type', 'application/json')
  const token = getToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)
  const res = await fetch(`/api/v1${path}`, { ...options, headers })
  if (res.status === 401) {
    clearSession()
    if (!location.pathname.startsWith('/login')) location.href = '/login'
  }
  const text = await res.text()
  let data: any = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = null
  }
  if (!res.ok) {
    throw new ApiError(res.status, data?.error || `Error HTTP ${res.status}`)
  }
  return data as T
}

export const apiGet = <T>(path: string) => api<{ success: boolean; data: T }>(path).then(r => r.data)
export const apiSend = <T>(path: string, method: string, body?: unknown) =>
  api<{ success: boolean; data: T }>(path, { method, body: body === undefined ? undefined : JSON.stringify(body) }).then(r => r.data)

export const apiUpload = <T>(path: string, form: FormData) =>
  api<{ success: boolean; data: T }>(path, { method: 'POST', body: form }).then(r => r.data)
