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
  const addons = { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` }
  const network = { label: 'Network', href: `/clusters/${c.cluster}/network` }
  const workloads = { label: 'Workloads', href: `/clusters/${c.cluster}/workloads` }
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
    case 'node.memory-small':
      return { title: 'Node too small for the platform add-ons', steps: [
        { text: 'Give the machine at least 2 GiB, or replace it.', link: node },
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
    case 'etcd.members':
      return { title: 'etcd membership changed', steps: [
        { text: 'Every control plane should be a member.', link: nodes },
      ] }
    case 'lb.lost':
      return { title: 'Ingress lost its LoadBalancer address', steps: [
        { text: 'Check the metallb-system workloads and the pool.', link: network },
      ] }
    case 'lb.pool-exhausted':
      return { title: 'MetalLB pool exhausted', steps: [
        { text: 'Delete services that no longer need an address.', link: network },
        { text: 'Or widen the MetalLB range in cluster.yaml and apply.', link: addons },
      ] }
    case 'machine.ip-changed':
      return { title: 'Machine moved to a new address', steps: [
        { text: 'Update its ip in cluster.yaml, or pin a static address.', link: nodes },
      ] }
    case 'backup.stale':
      return { title: 'etcd snapshots are behind schedule', steps: [
        { text: 'Take one: kubit etcd snapshot.', link: backups },
      ] }
    case 'cert.expiring':
      return { title: 'Credential expiring', steps: [
        { text: 'Renew it before it expires.', link: { label: 'Config', href: `/clusters/${c.cluster}/config` } },
      ] }
    case 'workload.unavailable':
      return { title: 'Workload below desired replicas', steps: [
        { text: 'Open its pods; logs and events are in the pod dialog.', link: workloads },
        { text: 'Pending pods: check node capacity and taints.' },
      ] }
    case 'pod.crashloop':
      return { title: 'Pod crashlooping', steps: [
        { text: 'Read the pod log and events.', link: workloads },
      ] }
    case 'flux.not-ready':
      return { title: 'Flux sync failing', steps: [
        { text: 'Read the error on the Flux card.', link: addons },
        { text: 'Fix it in the apps repository and push.' },
      ] }
    case 'pvc.pending':
      return { title: 'PersistentVolumeClaim stuck Pending', steps: [
        { text: "Check the storage classes and the claim's class.", link: { label: 'Storage', href: `/clusters/${c.cluster}/storage` } },
      ] }
    case 'service.no-endpoints':
      return { title: 'Service without endpoints', steps: [
        { text: "Compare the selector with the pods' labels and readiness.", link: network },
      ] }
    case 'ingress.no-address':
      return { title: 'Ingress has no address', steps: [
        { text: 'Check the Traefik add-on and its address.', link: addons },
      ] }
    default:
      return null
  }
}
