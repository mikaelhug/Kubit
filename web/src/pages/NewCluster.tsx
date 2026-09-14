import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type NodeRow } from '../api'
import { operations, opEvents, watch } from '../store'
import { ErrorBox, EventLog, Field, Pill } from '../components/ui'
import { OperationView } from '../components/ActivityDrawer'

type Step = 'discover' | 'declare' | 'create'

/** Create-cluster wizard: discover → pick nodes → edit the drafted cluster.yaml → create. */
export function NewCluster() {
  const [step, setStep] = useState<Step>('discover')
  const [targets, setTargets] = useState('192.168.1.0/24')
  const [discoverOp, setDiscoverOp] = useState<number | null>(null)
  const [nodes, setNodes] = useState<NodeRow[]>([])
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [name, setName] = useState('homelab')
  const [yaml, setYaml] = useState('')
  const [topology, setTopology] = useState<string>('')
  const [createOp, setCreateOp] = useState<number | null>(null)
  const [skipPlatform, setSkipPlatform] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const discoverStatus = discoverOp !== null ? operations.value.get(discoverOp) : undefined
  const discoverEvents = discoverOp !== null ? (opEvents.value.get(discoverOp) ?? []) : []

  const loadNodes = () => api.nodes().then((ns) => setNodes(ns.filter((n) => n.state === 'maintenance' && !n.cluster))).catch((e) => setError(e.message))
  useEffect(() => { loadNodes() }, [])
  useEffect(() => { if (discoverStatus && discoverStatus.status !== 'running') loadNodes() }, [discoverStatus?.status])
  useEffect(() => {
    // Default the scan target to the subnet of the first known node.
    if (nodes.length > 0 && targets === '192.168.1.0/24') setTargets(nodes[0].ip.replace(/\.\d+$/, '.0/24'))
  }, [nodes]) // eslint-disable-line

  const toggle = (ip: string) => setPicked((p) => { const n = new Set(p); n.has(ip) ? n.delete(ip) : n.add(ip); return n })
  const draft = () => api.draft(name, [...picked]).then((d) => {
    setYaml(d.yaml)
    const t = d.topology
    setTopology(`${t.ControlPlanes} control plane${t.ControlPlanes > 1 ? 's' : ''}${t.HA ? ' (etcd HA)' : ''}, ${t.Workers} worker${t.Workers === 1 ? '' : 's'}${t.AllowScheduling ? ', control planes schedulable' : ', dedicated control planes'}`)
    setStep('declare')
    setError(null)
  }).catch((e) => setError(e.message))
  const create = () => api.validate(yaml).then((v) => api.createCluster(v.yaml, skipPlatform)).then((r) => { setCreateOp(r.operationId); watch(r, false); setStep('create'); setError(null) }).catch((e) => setError(e.message))

  return (
    <div class="p-6 max-w-[960px] flex flex-col gap-5">
      <header class="flex items-center gap-4">
        <h1 class="text-xl font-semibold">New cluster</h1>
        <ol class="flex gap-2 text-[12px]">
          {(['discover', 'declare', 'create'] as Step[]).map((s, i) => (
            <li key={s} class={`flex items-center gap-1.5 ${s === step ? 'text-text' : 'text-muted'}`}>
              <span class={`inline-flex h-5 w-5 items-center justify-center rounded-full border text-[11px] ${s === step ? 'border-accent text-accent' : 'border-border'}`}>{i + 1}</span>
              {s === 'discover' ? 'Discover nodes' : s === 'declare' ? 'Declare cluster' : 'Create'}
            </li>
          ))}
        </ol>
      </header>
      <ErrorBox error={error} />

      {step === 'discover' && (
        <>
          <div class="panel p-4 flex flex-col gap-3">
            <p class="text-[13px] text-muted">Machines booted from a Talos ISO (or via PXE) sit in maintenance mode on port 50000. Scan the subnet to find them.</p>
            <div class="flex gap-2">
              <input class="input mono" value={targets} onInput={(e) => setTargets((e.target as HTMLInputElement).value)} placeholder="192.168.1.0/24, 10.0.0.5" />
              <button class="btn btn-primary shrink-0" disabled={discoverStatus?.status === 'running'} onClick={() => api.discover(targets.split(/[,\s]+/).filter(Boolean)).then((r) => setDiscoverOp(r.operationId)).catch((e) => setError(e.message))}>Scan</button>
            </div>
            {discoverOp !== null && <EventLog events={discoverEvents} empty="Scanning…" />}
          </div>
          <div class="panel overflow-x-auto">
            <table class="data">
              <thead><tr><th class="pl-4 w-8"></th><th>IP</th><th>MAC</th><th>Arch</th><th>Talos</th><th>CPU</th><th>RAM</th><th>Disks</th><th>KVM</th><th>Hardware</th></tr></thead>
              <tbody>
                {nodes.length === 0 && <tr><td colSpan={10} class="pl-4 text-muted">No nodes in maintenance mode known yet.</td></tr>}
                {nodes.map((n) => (
                  <tr key={n.ip} class="cursor-pointer" onClick={() => toggle(n.ip)}>
                    <td class="pl-4"><input type="checkbox" class="pointer-events-none" checked={picked.has(n.ip)} readOnly /></td>
                    <td class="mono">{n.ip}</td><td class="mono">{n.mac}</td><td>{n.arch}</td><td class="mono">{n.talosVersion}</td>
                    <td class="num">{n.inventory?.cpus ?? '—'}</td><td class="num">{fmt.bytes(n.inventory?.memoryBytes ?? 0)}</td>
                    <td class="mono">{n.inventory?.disks.filter((d) => !d.readonly && !d.cdrom && d.transport !== 'usb').map((d) => `${d.devPath} ${fmt.bytes(d.sizeBytes)}`).join(', ')}</td>
                    <td>{n.inventory?.kvm ? <Pill tone="good">yes</Pill> : <span class="text-muted">no</span>}</td>
                    <td class="text-muted truncate max-w-[200px]">{[n.inventory?.manufacturer, n.inventory?.product].filter(Boolean).join(' ')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div class="flex items-center gap-3">
            <Field label="Cluster name"><input class="input w-56" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} /></Field>
            <button class="btn btn-primary self-end" disabled={picked.size === 0 || !name} onClick={draft}>Continue with {picked.size} node{picked.size === 1 ? '' : 's'} →</button>
          </div>
        </>
      )}

      {step === 'declare' && (
        <>
          <div class="panel p-4 flex flex-col gap-2">
            <div class="flex items-center gap-2"><span class="label">Recommended topology</span><span class="text-[13px]">{topology}</span></div>
            <p class="text-[13px] text-muted">Edit anything: roles, hostnames, install disks, the MetalLB range, add-ons. Validate fills in defaults and reports mistakes.</p>
          </div>
          <textarea class="input mono !text-[12px] h-[480px]" value={yaml} onInput={(e) => setYaml((e.target as HTMLTextAreaElement).value)} spellcheck={false} />
          <div class="flex items-center gap-3">
            <button class="btn" onClick={() => setStep('discover')}>← Back</button>
            <button class="btn" onClick={() => api.validate(yaml).then((v) => { setYaml(v.yaml); setError(null) }).catch((e) => setError(e.message))}>Validate</button>
            <label class="ml-auto flex items-center gap-2 text-[13px]"><input type="checkbox" checked={skipPlatform} onChange={(e) => setSkipPlatform((e.target as HTMLInputElement).checked)} /> Skip platform add-ons</label>
            <button class="btn btn-primary" onClick={create}>Create cluster</button>
          </div>
        </>
      )}

      {step === 'create' && createOp !== null && (
        <div class="flex flex-col gap-3">
          <div class="panel h-[60vh] flex flex-col overflow-hidden"><OperationView id={createOp} tall /></div>
          <div class="flex gap-2 items-center">
            <a href={`/clusters/${name}/overview`} class="btn">Open cluster</a>
            <span class="text-[12px] text-muted">Provisioning keeps running if you leave; it stays in the Activity drawer.</span>
          </div>
        </div>
      )}
    </div>
  )
}
