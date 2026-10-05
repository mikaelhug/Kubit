import type { PlatformSpec } from './api'

export interface AddonInfo { key: keyof PlatformSpec | 'backup'; name: string; what: string; size: string; docs: string; hint?: string }

export const addonCatalog: AddonInfo[] = [
  { key: 'metallb', name: 'MetalLB', what: 'LoadBalancer addresses from the range, announced over ARP.', size: '~120 MiB, 1 controller + 1 speaker per node', docs: 'https://metallb.universe.tf/configuration/', hint: 'The pool range is a setting of its own.' },
  { key: 'traefik', name: 'Traefik', what: 'HTTP(S) ingress and Gateway API controller; the default IngressClass.', size: '~100 MiB, 1 pod', docs: 'https://github.com/traefik/traefik-helm-chart/blob/master/traefik/values.yaml', hint: 'Also serves Ingress objects of class nginx.' },
  { key: 'gvisor', name: 'gVisor', what: 'RuntimeClasses gvisor and gvisor-kvm for sandboxed pods.', size: 'no running pods', docs: 'https://gvisor.dev/docs/user_guide/containerd/quick_start/', hint: 'Plain manifests; no values.' },
  { key: 'metricsServer', name: 'metrics-server', what: 'Pod and node CPU and memory usage.', size: '~100 MiB, 1 pod', docs: 'https://github.com/kubernetes-sigs/metrics-server/blob/master/charts/metrics-server/values.yaml' },
  { key: 'certManager', name: 'cert-manager', what: 'X.509 certificates from ACME or internal CAs.', size: '~300 MiB, 3 pods', docs: 'https://cert-manager.io/docs/installation/helm/' },
  { key: 'longhorn', name: 'Longhorn', what: 'Replicated block storage; the default StorageClass.', size: '~1 GiB, 1 manager + engine per node', docs: 'https://longhorn.io/docs/latest/advanced-resources/deploy/customizing-default-settings/' },
  { key: 'builds', name: 'Builds', what: 'Builds images from the apps repository into a private registry.', size: '~200 MiB idle, more while building', docs: 'https://github.com/moby/buildkit', hint: 'Pulled as registry.kubit/<app>:<version>.' },
  { key: 'backup', name: 'talos-backup', what: 'Encrypted etcd snapshots to S3 on a schedule.', size: 'a CronJob, one short pod per run', docs: 'https://github.com/siderolabs/talos-backup', hint: 'Declared under spec.backup.' },
  { key: 'flux', name: 'Flux', what: 'GitOps sync from a Git repository.', size: '~150 MiB, 4 pods', docs: 'https://github.com/fluxcd-community/helm-charts/blob/main/charts/flux2/values.yaml' },
]
