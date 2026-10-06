import { readText } from '../local'

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) { super(message); this.status = status }
}

const token = readText('kubit.token')
export function getToken() { return token }

async function send<T>(method: string, path: string, body?: string, contentType?: string): Promise<T> {
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = 'Bearer ' + token
  if (contentType) headers['Content-Type'] = contentType
  const res = await fetch('/api/v1' + path, { method, headers, body })
  if (res.status === 204 || res.status === 202 && res.headers.get('content-length') === '0') return undefined as T
  const text = await res.text()
  let data: any = text
  try { data = JSON.parse(text) } catch {}
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || text || res.statusText)
  return data as T
}

export function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  return body === undefined ? send<T>(method, path) : send<T>(method, path, JSON.stringify(body), 'application/json')
}

export function authedUrl(path: string, params = new URLSearchParams()) {
  if (token) params.set('token', token)
  const q = params.toString()
  return q ? `/api/v1${path}?${q}` : `/api/v1${path}`
}
