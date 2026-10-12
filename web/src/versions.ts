import type { Versions } from './api'

function verLess(a: string, b: string): boolean {
  if (b.includes('-')) return false
  const pa = a.replace(/^v/, '').split('-')[0].split('.').map(Number), pb = b.replace(/^v/, '').split('.').map(Number)
  for (let i = 0; i < 3; i++) { if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) < (pb[i] ?? 0) }
  return false
}

export function latestStable(v?: Versions | null) { return v?.talos.find((x) => !x.includes('-')) }

export function talosUpdate(talos: string, v?: Versions | null) {
  const latest = latestStable(v)
  return latest && verLess(talos, latest) ? latest : ''
}

export function updatesFor(talos: string, kubernetes: string, v?: Versions | null) {
  return { talos: talosUpdate(talos, v), kubernetes: v && verLess(kubernetes, v.kubernetesLatest) ? v.kubernetesLatest : '' }
}

export const updateText = (talos: string, kubernetes: string) => [talos && `Talos ${talos}`, kubernetes && `Kubernetes ${kubernetes}`].filter(Boolean).join(' and ')
