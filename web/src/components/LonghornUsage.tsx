import { fmt, type LonghornNode, type LonghornView } from '../api'
import { type Tone } from '../tone'
import { Bar, Pill, UsedOf } from './ui'

export const longhornCapacity = (l: LonghornNode) => Math.max(0, l.maximumBytes - l.reservedBytes)

const health: [string, Tone][] = [['degraded', 'warn'], ['faulted', 'bad'], ['detached', 'muted']]

export function LonghornUsage({ view, href }: { view: LonghornView; href: string }) {
  const replicas = view.applied
  const volumes = Object.values(view.volumes).reduce((a, b) => a + b, 0)
  const nodes = view.nodes.filter((n) => n.live)
  return (
    <div class="panel p-3 flex flex-col gap-4">
      <div class="flex items-center gap-2">
        <a class="label hover:underline" href={href}>Storage</a>
        <span class="ml-auto text-[12px] text-muted">Longhorn{replicas ? ` · ${replicas} ${replicas === 1 ? 'replica' : 'replicas'}` : ''}</span>
      </div>
      <div class="flex flex-col gap-1.5">
        <div class="flex items-baseline justify-between gap-3">
          <span class="label">Left to provision</span>
          {replicas ? <span class="text-[13px]"><strong>{fmt.bytes(view.leftBytes)}</strong> <span class="text-muted">of {fmt.bytes(view.usableBytes)} usable</span></span> : <span class="text-muted">—</span>}
        </div>
        <Bar used={view.provisionedBytes} cap={view.provisionedBytes + view.leftBytes} />
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px]">
          <span><span class="text-muted">Provisioned</span> {fmt.bytes(view.provisionedBytes)}</span>
          <span><span class="text-muted">Written</span> {fmt.bytes(view.writtenBytes)}</span>
          <span><span class="text-muted">Volumes</span> {volumes}</span>
          {health.map(([state, tone]) => view.volumes[state] ? <Pill key={state} tone={tone}>{view.volumes[state]} {state}</Pill> : null)}
        </div>
      </div>
      {nodes.length > 0 && <span class="label">Scheduled per node</span>}
      {nodes.length > 0 && (
        <div class="grid grid-cols-[minmax(0,max-content)_1fr_max-content] items-center gap-x-3 gap-y-2 text-[12px]">
          {nodes.map((n) => {
            const l = n.live!
            const cap = longhornCapacity(l)
            return [
              <span key={n.hostname + 'n'} class="mono truncate">{n.hostname}</span>,
              <Bar key={n.hostname + 'b'} used={l.scheduledBytes} cap={cap} />,
              <span key={n.hostname + 'v'} class="text-right"><UsedOf used={l.scheduledBytes} cap={cap} /></span>,
            ]
          })}
        </div>
      )}
    </div>
  )
}
