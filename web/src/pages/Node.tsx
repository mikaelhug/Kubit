import { useEffect, useRef, useState } from 'preact/hooks'
import { api, logsUrl, type NodeRow, type Service } from '../api'
import { Breadcrumbs, ErrorBox, Pill, stateTone } from '../components/ui'

export function NodePage({ ip }: { ip: string }) {
  const [node, setNode] = useState<NodeRow | null>(null)
  const [services, setServices] = useState<Service[]>([])
  const [service, setService] = useState('')
  const [follow, setFollow] = useState(false)
  const [log, setLog] = useState('')
  const [error, setError] = useState<string | null>(null)
  const abort = useRef<AbortController | null>(null)

  useEffect(() => {
    api.nodes().then((ns) => setNode(ns.find((n) => n.ip === ip) ?? null)).catch((e) => setError(e.message))
    api.services(ip).then(setServices).catch((e) => setError(e.message))
  }, [ip])

  useEffect(() => {
    abort.current?.abort()
    const ac = new AbortController()
    abort.current = ac
    setLog('')
    fetch(logsUrl(ip, service, follow), { signal: ac.signal }).then(async (res) => {
      if (!res.ok) throw new Error(await res.text())
      const reader = res.body!.getReader()
      const dec = new TextDecoder()
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        setLog((prev) => (prev + dec.decode(value)).slice(-200000))
      }
    }).catch((e) => { if (e.name !== 'AbortError') setError(e.message) })
    return () => ac.abort()
  }, [ip, service, follow])

  return (
    <div class="p-6 flex flex-col gap-4 max-w-[1200px]">
      <Breadcrumbs items={node?.cluster ? [{ label: node.cluster, href: `/clusters/${node.cluster}/overview` }, { label: 'Nodes', href: `/clusters/${node.cluster}/nodes` }, { label: node.hostname || ip }] : [{ label: 'Inventory', href: '/fleet/inventory' }, { label: ip }]} />
      <header class="flex items-center gap-3">
        <h1 class="text-xl font-semibold">{node?.hostname || ip}</h1>
        <span class="mono text-muted">{ip} · {node?.mac} · {node?.arch} · Talos {node?.talosVersion}</span>
        {node?.cluster && <a href={`/clusters/${node.cluster}`} class="text-accent hover:underline">{node.cluster}</a>}
        <button class="btn ml-auto" onClick={() => { if (confirm(`Reboot ${node?.hostname || ip}?`)) api.reboot(ip).catch((e) => setError(e.message)) }}>Reboot</button>
      </header>
      <ErrorBox error={error} />
      <div class="grid grid-cols-1 lg:grid-cols-[320px_1fr] gap-4">
        <div class="panel overflow-x-auto">
          <table class="data">
            <thead><tr><th class="pl-4">Service</th><th>State</th><th class="pr-4"></th></tr></thead>
            <tbody>
              {services.map((s) => (
                <tr key={s.id} class={`cursor-pointer ${service === s.id ? 'bg-panel-2' : ''}`} onClick={() => setService(service === s.id ? '' : s.id)}>
                  <td class="pl-4 mono">{s.id}</td>
                  <td><Pill tone={s.healthy ? 'good' : s.state === 'Running' ? 'warn' : stateTone(s.state.toLowerCase())}>{s.state}{s.healthy ? '' : ' · unhealthy'}</Pill></td>
                  <td class="pr-4 text-muted text-[12px] truncate max-w-[120px]" title={s.last}>{s.last}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div class="panel p-4 flex flex-col gap-3 min-w-0">
          <div class="flex items-center gap-3">
            <h2 class="font-semibold">{service ? `${service} log` : 'Kernel log (dmesg)'}</h2>
            <label class="ml-auto flex items-center gap-2 text-[13px]"><input type="checkbox" checked={follow} onChange={(e) => setFollow((e.target as HTMLInputElement).checked)} /> Follow</label>
          </div>
          <pre class="log !max-h-[70vh]" ref={(el) => { if (el && follow) el.scrollTop = el.scrollHeight }}>{log || 'Loading…'}</pre>
        </div>
      </div>
    </div>
  )
}
