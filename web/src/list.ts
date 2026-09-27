import { splitList } from './api/format'

export const joinList = (v?: string[]) => (v ?? []).join(', ')

export const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i])

export const syncListText = (text: string, value?: string[]) => (sameList(splitList(text), value ?? []) ? text : joinList(value))

export function parseKV(text: string): Record<string, string> | undefined {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const s = line.trim()
    if (!s) continue
    const i = s.indexOf('=')
    if (i <= 0) out[s] = ''
    else out[s.slice(0, i).trim()] = s.slice(i + 1).trim()
  }
  return Object.keys(out).length ? out : undefined
}

export const kvLines = (m?: Record<string, string>) => Object.entries(m ?? {}).map(([k, v]) => (v === '' ? k : `${k}=${v}`)).join('\n')

export const syncKVText = (text: string, value?: Record<string, string>) => (JSON.stringify(parseKV(text) ?? {}) === JSON.stringify(value ?? {}) ? text : kvLines(value))

const csvField = (s: string) => (/[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s)

export const csvLine = (fields: string[]) => fields.map(csvField).join(',')
