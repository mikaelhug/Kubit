import { useCallback, useEffect, useState } from 'preact/hooks'
import { api, type Operation } from '../api'
import { useOperationUpdates } from '../events'
import { Pill, stateTone } from '../components/ui'
import { OperationPanel } from '../components/OperationPanel'

export function Operations({ id }: { id?: string }) {
  const [ops, setOps] = useState<Operation[]>([])
  const [selected, setSelected] = useState<Operation | null>(null)
  const reload = useCallback(() => api.operations().then(setOps).catch(() => {}), [])
  useEffect(() => { reload() }, [reload])
  useOperationUpdates(useCallback(() => { reload() }, [reload]))
  useEffect(() => { if (id) api.operation(Number(id)).then(setSelected).catch(() => {}); else setSelected(null) }, [id])

  return (
    <div class="p-6 flex flex-col gap-4 max-w-[1200px]">
      <h1 class="text-xl font-semibold">Operations</h1>
      <div class="grid grid-cols-1 lg:grid-cols-[420px_1fr] gap-4">
        <div class="panel overflow-x-auto">
          <table class="data">
            <thead><tr><th class="pl-4">#</th><th>Kind</th><th>Cluster</th><th>Status</th><th class="pr-4">Started</th></tr></thead>
            <tbody>
              {ops.length === 0 && <tr><td colSpan={5} class="pl-4 text-muted">Nothing has run yet.</td></tr>}
              {ops.map((o) => (
                <tr key={o.id} class={`cursor-pointer ${selected?.id === o.id ? 'bg-panel-2' : ''}`} onClick={() => { history.pushState(null, '', `/operations/${o.id}`); api.operation(o.id).then(setSelected) }}>
                  <td class="pl-4 num">{o.id}</td><td class="mono">{o.kind}</td><td>{o.cluster || <span class="text-muted">—</span>}</td>
                  <td><Pill tone={stateTone(o.status)}>{o.status}</Pill></td>
                  <td class="pr-4 text-muted num">{new Date(o.startedAt).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div class="panel p-4 min-w-0">
          {!selected && <span class="text-muted text-[13px]">Select an operation to read its log.</span>}
          {selected && selected.status === 'running' && <OperationPanel id={selected.id} title={selected.kind} />}
          {selected && selected.status !== 'running' && (
            <div class="flex flex-col gap-2">
              <div class="flex items-center gap-2"><span class="font-medium">{selected.kind}</span><span class="text-muted text-[12px]">#{selected.id}</span><Pill tone={stateTone(selected.status)}>{selected.status}</Pill></div>
              <pre class="log !max-h-[70vh]">{selected.log || '(empty)'}</pre>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
