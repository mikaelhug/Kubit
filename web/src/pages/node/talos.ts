import { api, type NodeRow } from '../../api'
import { useLive } from '../../useLive'

export function talosLive(node: NodeRow | null) {
  return { scopes: [[node?.cluster ?? '', 'nodes']] as const, refresh: [node?.cluster ? '' : node?.lastSeen] }
}

export function useServices(ip: string, node: NodeRow | null) {
  const talos = talosLive(node)
  return useLive(() => api.services(ip), [ip], talos.scopes, { refresh: talos.refresh })
}
