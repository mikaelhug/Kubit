import { today } from '../clock'

const kinds: Record<string, string> = {
  'cluster.create': 'Create cluster', 'cluster.apply': 'Apply cluster.yaml', 'platform.plan': 'Plan add-ons', 'platform.apply': 'Apply add-ons',
  'upgrade.talos': 'Upgrade Talos', 'upgrade.kubernetes': 'Upgrade Kubernetes', 'node.add': 'Add node', 'node.remove': 'Remove node', discover: 'Discover nodes',
  'etcd.snapshot': 'etcd snapshot', 'etcd.restore': 'Restore etcd from snapshot',
}

export const splitList = (s: string) => s.split(/[,\s]+/).filter(Boolean)

export const fmt = {
  bytes(b: number) {
    if (!b) return '0'
    const u = ['B', 'K', 'M', 'G', 'T']
    let i = 0
    let v = b
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
    return (i >= 3 && v < 100 ? v.toFixed(1) : Math.round(v)) + u[i]
  },
  cores(m: number) { return m >= 1000 ? (m / 1000).toFixed(1) : `${m}m` },
  pct(a: number, b: number) { return b ? Math.round((a / b) * 100) : 0 },
  int(n: number) { return n.toLocaleString() },
  date(iso: string) { return iso ? new Date(iso).toLocaleDateString() : '' },
  datetime(iso: string) { return iso ? new Date(iso).toLocaleString() : '' },
  when(iso: string) {
    if (!iso) return ''
    const d = new Date(iso)
    return d.toDateString() === today.value ? d.toLocaleTimeString() : d.toLocaleString()
  },
  weekday(ms: number) { return new Date(ms).toLocaleDateString(undefined, { weekday: 'short' }) },
  hm(ms: number) { return new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) },
  span(ms: number) {
    if (ms < 1000) return '<1s'
    const s = Math.round(ms / 1000)
    if (s < 60) return `${s}s`
    const m = Math.floor(s / 60)
    if (m < 60) return `${m}m ${s % 60}s`
    const h = Math.floor(m / 60)
    if (h < 24) return `${h}h ${m % 60}m`
    return `${Math.floor(h / 24)}d ${h % 24}h`
  },
  duration(from?: string, to?: string, now = Date.now()) {
    if (!from) return ''
    return fmt.span((to ? Date.parse(to) : now) - Date.parse(from))
  },
  age(sec: number) {
    const s = Math.max(0, Math.floor(sec))
    if (s < 60) return `${s} s`
    if (s < 3600) return `${Math.round(s / 60)} min`
    if (s < 86400) return `${Math.round(s / 3600)} h`
    return `${Math.round(s / 86400)} d`
  },
  kind(kind: string) { return kinds[kind] || kind },
}
