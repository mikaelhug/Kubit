import { useEffect, useRef } from 'preact/hooks'

export interface Point { t: number; v: number }

/**
 * Area chart on canvas, drawn with the theme's tokens. Gaps larger than 3× the median
 * interval are left empty so an outage reads as a hole, not a slope.
 */
export function Sparkline({ points, max, height = 56, format, label }: { points: Point[]; max?: number; height?: number; format: (v: number) => string; label: string }) {
  const ref = useRef<HTMLCanvasElement>(null)
  useEffect(() => {
    const canvas = ref.current
    if (!canvas) return
    const css = getComputedStyle(document.documentElement)
    const accent = css.getPropertyValue('--accent').trim()
    const muted = css.getPropertyValue('--muted').trim()
    const border = css.getPropertyValue('--border').trim()
    const dpr = window.devicePixelRatio || 1
    const w = canvas.clientWidth
    canvas.width = w * dpr
    canvas.height = height * dpr
    const ctx = canvas.getContext('2d')!
    ctx.scale(dpr, dpr)
    ctx.clearRect(0, 0, w, height)
    if (points.length < 2) {
      ctx.fillStyle = muted
      ctx.font = '11px system-ui'
      ctx.fillText('collecting…', 4, height / 2)
      return
    }
    const t0 = points[0].t, t1 = points[points.length - 1].t
    const top = (max ?? Math.max(...points.map((p) => p.v)) * 1.1) || 1
    const x = (t: number) => ((t - t0) / Math.max(1, t1 - t0)) * (w - 2) + 1
    const y = (v: number) => height - 4 - (v / top) * (height - 10)
    const gaps = points.slice(1).map((p, i) => p.t - points[i].t).sort((a, b) => a - b)
    const median = gaps[Math.floor(gaps.length / 2)] || 1
    ctx.strokeStyle = border
    ctx.lineWidth = 1
    for (const f of [0.5, 1]) { ctx.beginPath(); ctx.moveTo(0, y(top * f / 1.1)); ctx.lineTo(w, y(top * f / 1.1)); ctx.stroke() }
    ctx.lineWidth = 1.5
    let segment: Point[] = []
    const flush = () => {
      if (segment.length < 2) { segment = []; return }
      ctx.beginPath()
      segment.forEach((p, i) => i ? ctx.lineTo(x(p.t), y(p.v)) : ctx.moveTo(x(p.t), y(p.v)))
      ctx.strokeStyle = accent
      ctx.stroke()
      ctx.lineTo(x(segment[segment.length - 1].t), height - 4)
      ctx.lineTo(x(segment[0].t), height - 4)
      ctx.closePath()
      ctx.fillStyle = accent + '33'
      ctx.fill()
      segment = []
    }
    points.forEach((p, i) => {
      if (i > 0 && p.t - points[i - 1].t > median * 3) flush()
      segment.push(p)
    })
    flush()
    const last = points[points.length - 1]
    ctx.fillStyle = accent
    ctx.beginPath(); ctx.arc(x(last.t), y(last.v), 2.5, 0, Math.PI * 2); ctx.fill()
  }, [points, max, height])
  const last = points[points.length - 1]
  return (
    <div class="flex flex-col gap-1">
      <div class="flex items-baseline justify-between">
        <span class="label">{label}</span>
        <span class="num text-[13px]">{last ? <><strong>{format(last.v)}</strong>{max ? <span class="text-muted"> / {format(max)}</span> : null}</> : '—'}</span>
      </div>
      <canvas ref={ref} class="w-full" style={{ height }} />
    </div>
  )
}
