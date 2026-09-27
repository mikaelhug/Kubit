import { readText } from '../local'

class ApiError extends Error {
  status: number
  code?: string
  command?: string
  body?: any
  constructor(status: number, message: string, extra?: { code?: string; command?: string; body?: any }) { super(message); this.status = status; this.code = extra?.code; this.command = extra?.command; this.body = extra?.body }
}

const token = readText('kubit.token')
export function getToken() { return token }

let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void) { onUnauthorized = fn }

async function send<T>(method: string, path: string, body?: string, contentType?: string): Promise<T> {
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = 'Bearer ' + token
  if (contentType) headers['Content-Type'] = contentType
  const res = await fetch('/api/v1' + path, { method, headers, body })
  if (res.status === 204 || res.status === 202 && res.headers.get('content-length') === '0') return undefined as T
  const text = await res.text()
  let data: any = text
  try { data = JSON.parse(text) } catch {}
  if (res.status === 401 && !path.startsWith('/auth/')) onUnauthorized()
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || text || res.statusText, data && typeof data === 'object' ? { code: data.code, command: data.command, body: data } : undefined)
  return data as T
}

export function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  return body === undefined ? send<T>(method, path) : send<T>(method, path, JSON.stringify(body), 'application/json')
}

export function reqRaw<T>(method: string, path: string, text: string, contentType = 'application/yaml'): Promise<T> {
  return send<T>(method, path, text, contentType)
}

export function authedUrl(path: string, params = new URLSearchParams()) {
  if (token) params.set('token', token)
  const q = params.toString()
  return q ? `/api/v1${path}?${q}` : `/api/v1${path}`
}
