import type { SecretEntry, SecretFile } from './api'

export type Section = 'data' | 'stringData' | ''

export interface Row {
  id: string
  section: Section
  key: string
  value: string
  binary?: number
  from?: { section: Section; key: string; value: string }
}

export interface Review { added: string[]; changed: string[]; removed: string[]; renamed: [string, string][] }

export const isSecret = (f: Pick<SecretFile, 'kind'>) => f.kind === 'Secret'

const utf8 = new TextDecoder('utf-8', { fatal: true })

function decodeData(b64: string): { text: string; binary?: number } {
  let bytes: Uint8Array
  try {
    bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
  } catch {
    return { text: b64, binary: 0 }
  }
  try {
    return { text: utf8.decode(bytes) }
  } catch {
    return { text: b64, binary: bytes.length }
  }
}

function encodeData(text: string) {
  let bin = ''
  for (const b of new TextEncoder().encode(text)) bin += String.fromCharCode(b)
  return btoa(bin)
}

let seq = 0
const nextId = () => `r${++seq}`

export function rowsOf(file: Pick<SecretFile, 'kind'>, values: SecretEntry[]): Row[] {
  const rows: Row[] = []
  for (const e of values) {
    if (isSecret(file)) {
      const [section, ...rest] = e.path
      if ((section !== 'data' && section !== 'stringData') || rest.length !== 1) continue
      const key = rest[0]
      const { text, binary } = section === 'data' ? decodeData(e.value) : { text: e.value, binary: undefined }
      rows.push({ id: nextId(), section, key, value: text, binary, from: { section, key, value: text } })
    } else {
      const key = e.path.join('.')
      rows.push({ id: nextId(), section: '', key, value: e.value, from: { section: '', key, value: e.value } })
    }
  }
  return rows
}

export function newRow(file: Pick<SecretFile, 'kind'>): Row {
  return { id: nextId(), section: isSecret(file) ? 'stringData' : '', key: '', value: '' }
}

const pathOf = (section: Section, key: string) => (section ? [section, key] : key.split('.'))

export function patchOf(before: Row[], after: Row[]): { set: SecretEntry[]; remove: string[][] } {
  const set: SecretEntry[] = []
  const remove: string[][] = []
  const kept = new Set(after.map((r) => r.id))
  for (const r of before) if (!kept.has(r.id) && r.from) remove.push(pathOf(r.from.section, r.from.key))
  for (const r of after) {
    if (r.binary !== undefined && r.from && r.key === r.from.key) continue
    const moved = r.from && r.from.key !== r.key
    if (moved) remove.push(pathOf(r.from!.section, r.from!.key))
    if (!r.from || moved || r.value !== r.from.value) {
      set.push({ path: pathOf(r.section, r.key.trim()), value: r.section === 'data' && r.binary === undefined ? encodeData(r.value) : r.value })
    }
  }
  return { set, remove }
}

export function reviewOf(before: Row[], after: Row[]): Review {
  const out: Review = { added: [], changed: [], removed: [], renamed: [] }
  const kept = new Set(after.map((r) => r.id))
  for (const r of before) if (!kept.has(r.id) && r.from) out.removed.push(r.from.key)
  for (const r of after) {
    if (!r.from) out.added.push(r.key.trim())
    else if (r.from.key !== r.key) out.renamed.push([r.from.key, r.key.trim()])
    else if (r.from.value !== r.value) out.changed.push(r.key)
  }
  return out
}

export const reviewEmpty = (r: Review) => !r.added.length && !r.changed.length && !r.removed.length && !r.renamed.length

export const required = 'Required'

function charProblem(v: string, max: number, allowed: RegExp, lowercase: boolean) {
  if (v.length > max) return `At most ${max} characters`
  if (lowercase && /[A-Z]/.test(v)) return 'Lowercase only'
  const bad = [...v].find((c) => !allowed.test(c))
  return bad === undefined ? '' : /\s/.test(bad) ? 'No spaces' : `'${bad}' is not allowed`
}

export function keyProblems(file: Pick<SecretFile, 'kind'>, rows: Row[]): Map<string, string> {
  const out = new Map<string, string>()
  const seen = new Map<string, string>()
  for (const r of rows) {
    const k = r.key.trim()
    const chars = isSecret(file) ? charProblem(k, 253, /[-._a-zA-Z0-9]/, false) : ''
    if (!k) out.set(r.id, required)
    else if (chars) out.set(r.id, chars)
    else if (!isSecret(file) && k.split('.').some((p) => !p)) out.set(r.id, 'Empty path segment')
    else if (seen.has(k)) out.set(r.id, 'Duplicate key')
    if (k) seen.set(k, r.id)
  }
  return out
}

function dnsProblem(v: string, max: number, dots: boolean, typing: boolean) {
  if (!v) return typing ? '' : required
  const chars = charProblem(v, max, dots ? /[-.a-z0-9]/ : /[-a-z0-9]/, true)
  if (chars || typing) return chars
  const parts = dots ? v.split('.') : [v]
  if (parts.every((p) => /^[a-z0-9](.*[a-z0-9])?$/.test(p))) return ''
  return parts.length > 1 ? "Start and end with a letter or digit, also around '.'" : 'Start and end with a letter or digit'
}

export const nameProblem = (name: string, typing = false) => dnsProblem(name, 253, true, typing)
export const namespaceProblem = (ns: string, typing = false) => (ns ? dnsProblem(ns, 63, false, typing) : '')

export function fileProblem(path: string) {
  if (!path) return required
  if (path.startsWith('/') || path.split('/').includes('..')) return 'Must stay inside the repo'
  return /\.sops\.ya?ml$/.test(path) ? '' : 'Must end in .sops.yaml'
}

export function defaultFile(root: string | undefined, namespace: string, name: string) {
  if (nameProblem(name) || namespaceProblem(namespace)) return ''
  const base = root === undefined ? 'apps' : root.replace(/^\.?\/*/, '').replace(/\/+$/, '')
  return [base, namespace || name, `${name}.sops.yaml`].filter(Boolean).join('/')
}

export function dockerConfig(server: string, username: string, password: string, email: string) {
  const auth = encodeData(`${username}:${password}`)
  return JSON.stringify({ auths: { [server]: { username, password, ...(email ? { email } : {}), auth } } })
}

export const typeLabel = (t?: string) => (t === 'kubernetes.io/tls' ? 'TLS' : t === 'kubernetes.io/dockerconfigjson' ? 'Registry' : t || 'Opaque')

export const gitLabel = (code?: string) => (!code ? '' : code === '??' ? 'untracked' : code.includes('A') ? 'added' : code.includes('D') ? 'deleted' : code.includes('R') ? 'renamed' : 'modified')
