import type { PlatformSpec } from './api'

export interface AddonInfo { key: keyof PlatformSpec | 'backup'; name: string }

export const addonCatalog: AddonInfo[] = [
  { key: 'metallb', name: 'MetalLB' },
  { key: 'traefik', name: 'Traefik' },
  { key: 'gvisor', name: 'gVisor' },
  { key: 'metricsServer', name: 'metrics-server' },
  { key: 'certManager', name: 'cert-manager' },
  { key: 'longhorn', name: 'Longhorn' },
  { key: 'builds', name: 'Builds' },
  { key: 'backup', name: 'talos-backup' },
  { key: 'flux', name: 'Flux' },
]
