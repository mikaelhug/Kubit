import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, fmt, type LabHost, type NodeRow, type Sample } from '../../api'
import { labOffline, vmsOf } from '../../machine'
import { hostSamples } from '../../store'
import { appendWithin } from '../../time'
import { levelTone } from '../../tone'
import { useLive } from '../../useLive'
import { RangeButtons, Sparkline, spanOf } from '../Sparkline'
import { Tile } from '../ui'
import { mib, totalMem } from './plan'

export function HostMetrics({ host, lh }: { host: NodeRow; lh: LabHost }) {
  const vms = vmsOf(lh)
  const [range, setRange] = useState('24h')
  const { data: samples, set } = useLive(() => api.labSamples(host.mac, range), [host.mac, range], [], { onError: 'silent' })
  const live = hostSamples.value.get(host.mac)
  useEffect(() => {
    if (live) set((prev) => appendWithin(prev, live, spanOf(range)))
  }, [live?.ts])
  const series = useMemo(() => {
    const pts = (f: (s: Sample) => number) => (samples ?? []).map((s) => ({ t: Date.parse(s.ts), v: s.reachable ? f(s) : null }))
    return { cpu: pts((s) => s.cpuMilli / 10), mem: pts((s) => s.memBytes), disk: pts((s) => s.disk ?? 0), vms: pts((s) => s.pods) }
  }, [samples])
  const m = lh.metrics
  const committed = vms.reduce((s, v) => s + v.diskGiB, 0)
  const diskPct = m && m.diskTotal ? fmt.pct(m.diskUsed, m.diskTotal) : 0
  const memPct = m && m.memTotal ? fmt.pct(m.memUsed, m.memTotal) : 0
  const offline = labOffline(lh)
  const graph = offline ? 'bad' : 'accent'
  const tone = (t?: ReturnType<typeof levelTone>) => (offline ? 'muted' : t)
  return (
    <div class="panel p-3 flex flex-col gap-3">
      <div class="flex items-center gap-2">
        <span class="label">Host utilisation</span>
        {m && !offline && <span class="text-[12px] text-muted">load {m.load1.toFixed(2)} · up {fmt.span(m.uptimeSec * 1000)}</span>}
        <RangeButtons value={range} onChange={setRange} />
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
        <Tile plain label="CPU" value={m ? `${Math.round(m.cpuPct)}%` : '—'} sub={`${lh.capacity.cpus} cores · ${vms.reduce((s, v) => s + v.cpus, 0)} vCPU assigned`} tone={tone(m ? levelTone(m.cpuPct, 80, 95) : undefined)}>
          <Sparkline label="" points={series.cpu} max={100} format={(v) => `${Math.round(v)}%`} height={44} tone={graph} />
        </Tile>
        <Tile plain label="Memory" value={m ? fmt.bytes(m.memUsed) : '—'} sub={m ? `of ${fmt.bytes(m.memTotal)} · ${fmt.bytes(mib(totalMem(vms)))} assigned to VMs` : ''} tone={tone(levelTone(memPct, 85, 92))}>
          <Sparkline label="" points={series.mem} max={m?.memTotal} format={fmt.bytes} height={44} tone={graph} />
        </Tile>
        <Tile plain label="VM disk" value={m ? `${diskPct}%` : '—'} sub={m ? `${fmt.bytes(m.diskUsed)} of ${fmt.bytes(m.diskTotal)} · VMs may grow to ${committed} GiB` : ''} tone={tone(levelTone(diskPct, 85, 95))}>
          <Sparkline label="" points={series.disk} max={m?.diskTotal} format={fmt.bytes} height={44} tone={graph} />
        </Tile>
        <Tile plain label="VMs running" value={offline ? '—' : m ? `${m.vmsRunning}/${vms.length}` : `${vms.filter((v) => v.state === 'running').length}/${vms.length}`} sub={`${vms.length} defined on this host`} tone={tone()}>
          <Sparkline label="" points={series.vms} max={Math.max(1, vms.length)} format={String} height={44} tone={graph} />
        </Tile>
      </div>
    </div>
  )
}
