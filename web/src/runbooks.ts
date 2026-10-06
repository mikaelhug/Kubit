interface RunbookStep { text: string; link?: { label: string; href: string } }
export interface Runbook { title: string; steps: RunbookStep[] }

interface Ctx { cluster: string; node?: string; nodeHref?: string }

export function runbookFor(kind: string, c: Ctx): Runbook | null {
  if (kind === 'observer.offline') {
    return { title: "Kubit's own host cannot reach the LAN", steps: [
      { text: 'Check the link and address of the machine running kubit.' },
      { text: 'macOS: allow kubit under System Settings → Privacy & Security → Local Network.' },
    ] }
  }
  const nodes = { label: 'Nodes', href: `/clusters/${c.cluster}/nodes` }
  const node = c.nodeHref ? { label: `Open ${c.node}`, href: c.nodeHref } : nodes
  const backups = { label: 'Backups', href: `/clusters/${c.cluster}/backups` }
  switch (kind) {
    case 'talos.unreachable':
      return { title: 'Machine not answering on the Talos API', steps: [
        { text: 'Check power and link.', link: node },
        { text: 'New DHCP address: update its ip in cluster.yaml and apply.' },
        { text: 'Gone for good: remove it from cluster.yaml and apply.' },
      ] }
    case 'node.notready':
      return { title: 'Kubelet reports NotReady', steps: [
        { text: 'Check its conditions and the kubelet service.', link: node },
        { text: 'Disk pressure: free space under /var or move workloads.' },
      ] }
    case 'api.unreachable':
      return { title: 'Kubernetes API unreachable', steps: [
        { text: 'Check whether the control planes answer on the Talos API.', link: nodes },
        { text: 'etcd quorum lost for good: kubit etcd restore.', link: backups },
      ] }
    case 'etcd.unhealthy':
      return { title: 'etcd has lost health', steps: [
        { text: 'Bring back the down control planes.', link: nodes },
        { text: 'Majority lost for good: kubit etcd restore.', link: backups },
      ] }
    case 'cert.expiring':
      return { title: 'Credential expiring', steps: [
        { text: 'Renew it before it expires.', link: { label: 'Certificates', href: `/clusters/${c.cluster}/repository?view=certs` } },
      ] }
    default:
      return null
  }
}
