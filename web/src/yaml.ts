export function toYaml(v: unknown, indent = 0): string {
  const pad = '  '.repeat(indent)
  if (Array.isArray(v)) return v.map((x) => typeof x === 'object' && x !== null ? `${pad}-\n${toYaml(x, indent + 1)}` : `${pad}- ${scalar(x)}`).join('\n')
  if (typeof v === 'object' && v !== null) {
    const entries = Object.entries(v as Record<string, unknown>)
    if (entries.length === 0) return ''
    return entries.map(([k, x]) => typeof x === 'object' && x !== null && Object.keys(x as object).length > 0 ? `${pad}${k}:\n${toYaml(x, indent + 1)}` : `${pad}${k}: ${typeof x === 'object' && x !== null ? (Array.isArray(x) ? '[]' : '{}') : scalar(x)}`).join('\n')
  }
  return pad + scalar(v)
}

function scalar(x: unknown) { return typeof x === 'string' ? (/^[\w./:-]+$/.test(x) && !/^(true|false|null|\d+)$/.test(x) ? x : JSON.stringify(x)) : String(x) }
