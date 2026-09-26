import type { Versions } from './api'

const vanillaSchematic = '376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba'

function verLess(a: string, b: string): boolean {
  if (b.includes('-')) return false
  const pa = a.replace(/^v/, '').split('-')[0].split('.').map(Number), pb = b.replace(/^v/, '').split('.').map(Number)
  for (let i = 0; i < 3; i++) { if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) < (pb[i] ?? 0) }
  return false
}

function latestStable(v?: Versions | null) { return v?.talos.find((x) => !x.includes('-')) }

export function defaultTalos(v?: Versions | null) { return latestStable(v) ?? v?.minTalos ?? 'v1.14.0' }

export function talosIso(factory: string, talos: string, arch: 'amd64' | 'arm64') { return `${factory}/image/${vanillaSchematic}/${talos}/metal-${arch}.iso` }

export function updatesFor(talos: string, kubernetes: string, v?: Versions | null) {
  const latest = latestStable(v)
  return { talos: latest && verLess(talos, latest) ? latest : '', kubernetes: v && verLess(kubernetes, v.kubernetesLatest) ? v.kubernetesLatest : '' }
}
