import { useEffect, useRef } from 'preact/hooks'

export interface Point { t: number; v: number | null }

const spans: Record<string, number> = { '1h': 3600e3, '6h': 6 * 3600e3, '24h': 86400e3, '7d': 7 * 86400e3 }
export const spanOf = (range: string) => spans[range] ?? spans['24h']

const tickSteps = [5, 10, 15, 30, 60, 120, 180, 360, 720, 1440, 2880].map((m) => m * 60e3)
const shareSteps = [0.05, 0.1, 0.2, 0.25, 0.5, 1]

/** The smallest round ceiling above v: a share of `max` from shareSteps, or 1-2-5 × 10ⁿ. */
function ceilingFor(v: number, max?: number): number {
  if (max) return (shareSteps.find((s) => s * max >= v * 1.02) ?? 1) * max
  if (v <= 0) return 1
  const p = Math.pow(10, Math.floor(Math.log10(v)))
  return ([1, 2, 5, 10].find((m) => m * p >= v) ?? 10) * p
}

/**
 * Utilisation over time on canvas: faded area under a solid line. The y axis runs
 * from zero to a round ceiling just above the busiest moment in view, so small
 * swings stay visible; with `span` the x axis covers exactly that window ending at the
 * newest sample and carries time ticks, without it the axis stretches over the
 * samples given (a compact sparkline). A null value (observed but unreachable) or a
 * silence longer than 3× the median interval breaks the line rather than being bridged.
 */
export function Sparkline({ points, max, height = 56, format, label, span, tone = 'accent' }: { points: Point[]; max?: number; height?: number; format: (v: number) => string; label: string; span?: number; tone?: 'accent' | 'bad' }) {
  const ref = useRef<HTMLCanvasElement>(null)
  useEffect(() => {
    const canvas = ref.current
    if (!canvas) return
    const css = getComputedStyle(document.documentElement)
    const accent = css.getPropertyValue(`--${tone}`).trim()
    const muted = css.getPropertyValue('--muted').trim()
    const border = css.getPropertyValue('--border').trim()
    const dpr = window.devicePixelRatio || 1
    const w = canvas.clientWidth
    canvas.width = w * dpr
    canvas.height = height * dpr
    const ctx = canvas.getContext('2d')!
    ctx.scale(dpr, dpr)
    ctx.clearRect(0, 0, w, height)
    ctx.font = '10px system-ui'
    const px0 = span ? 34 : 1, px1 = w - 2, py0 = 6, py1 = height - (span ? 15 : 4)
    const known = points.filter((p): p is { t: number; v: number } => p.v !== null)
    const t1 = points.length ? points[points.length - 1].t : Date.now()
    const t0 = span ? t1 - span : points.length ? points[0].t : t1 - 1
    const x = (t: number) => px0 + ((t - t0) / Math.max(1, t1 - t0)) * (px1 - px0)

    const gaps = points.slice(1).map((p, i) => p.t - points[i].t).sort((a, b) => a - b)
    const median = gaps[Math.floor(gaps.length / 2)] || 1
    const segments: { x: number; v: number }[][] = []
    let column: { x: number; sum: number; n: number }[] = []
    const flush = () => {
      if (column.length) segments.push(column.map((c) => ({ x: c.x, v: c.sum / c.n })))
      column = []
    }
    points.forEach((p, i) => {
      if (p.v === null || (i > 0 && p.t - points[i - 1].t > median * 3)) flush()
      if (p.v === null || p.t < t0) return
      const cx = Math.round(x(p.t))
      const last = column[column.length - 1]
      if (last && last.x === cx) { last.sum += p.v; last.n++ } else column.push({ x: cx, sum: p.v, n: 1 })
    })
    flush()
    const peak = segments.reduce((m, s) => s.reduce((m2, p) => Math.max(m2, p.v), m), 0)
    const top = ceilingFor(peak, max)
    const y = (v: number) => py1 - (Math.min(v, top) / top) * (py1 - py0)
    const pct = (v: number) => `${Math.round((v / max!) * 100)}%`

    ctx.strokeStyle = border
    ctx.lineWidth = 1
    ctx.fillStyle = muted
    ctx.textBaseline = 'middle'
    ctx.textAlign = 'right'
    for (const f of [0.5, 1]) {
      const gy = Math.round(y(top * f)) + 0.5
      ctx.beginPath(); ctx.moveTo(px0, gy); ctx.lineTo(px1, gy); ctx.stroke()
      if (span) ctx.fillText(max ? pct(top * f) : format(top * f), px0 - 5, gy)
    }
    if (span) {
      const step = tickSteps.find((s) => span / s <= 4) ?? tickSteps[tickSteps.length - 1]
      const off = new Date(t1).getTimezoneOffset() * 60e3
      ctx.textBaseline = 'top'
      ctx.textAlign = 'center'
      for (let t = Math.ceil((t0 - off) / step) * step + off; t <= t1; t += step) {
        const d = new Date(t)
        const text = step >= 86400e3 ? d.toLocaleDateString(undefined, { weekday: 'short' }) : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
        ctx.fillText(text, Math.min(Math.max(x(t), px0 + 16), px1 - 16), py1 + 4)
      }
    }
    if (known.length < 2) {
      ctx.textBaseline = 'middle'
      ctx.textAlign = 'left'
      ctx.fillText('collecting…', px0 + 4, (py0 + py1) / 2)
      return
    }
    ctx.lineWidth = 1.5
    ctx.lineJoin = 'round'
    for (const s of segments) {
      if (s.length === 1) {
        ctx.fillStyle = accent
        ctx.beginPath(); ctx.arc(s[0].x, y(s[0].v), 1.5, 0, Math.PI * 2); ctx.fill()
        continue
      }
      ctx.beginPath()
      s.forEach((p, i) => i ? ctx.lineTo(p.x, y(p.v)) : ctx.moveTo(p.x, y(p.v)))
      ctx.lineTo(s[s.length - 1].x, py1)
      ctx.lineTo(s[0].x, py1)
      ctx.closePath()
      ctx.fillStyle = accent + '2e'
      ctx.fill()
      ctx.beginPath()
      s.forEach((p, i) => i ? ctx.lineTo(p.x, y(p.v)) : ctx.moveTo(p.x, y(p.v)))
      ctx.strokeStyle = accent
      ctx.stroke()
    }
    const last = points[points.length - 1]
    if (last.v !== null) {
      ctx.fillStyle = accent
      ctx.beginPath(); ctx.arc(x(last.t), y(last.v), 2.5, 0, Math.PI * 2); ctx.fill()
    }
  }, [points, max, height, span, tone])
  const last = points.filter((p) => p.v !== null).pop()
  return (
    <div class="flex flex-col gap-1">
      {label && <div class="flex items-baseline justify-between">
        <span class="label">{label}</span>
        <span class="num text-[13px]">{last ? <><strong>{format(last.v!)}</strong>{max ? <span class="text-muted"> / {format(max)} · {Math.round((last.v! / max) * 100)}%</span> : null}</> : '—'}</span>
      </div>}
      <canvas ref={ref} class="w-full" style={{ height }} />
    </div>
  )
}
