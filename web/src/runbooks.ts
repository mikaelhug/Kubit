interface RunbookStep { text: string; link?: { label: string; href: string } }
export interface Runbook { title: string; steps: RunbookStep[] }

interface Ctx { cluster: string; node?: string; nodeHref?: string }

export function runbookFor(kind: string, c: Ctx): Runbook | null {
  if (c.cluster.startsWith('labhost:')) return labRunbook(kind, c.cluster.slice('labhost:'.length))
  if (kind === 'observer.offline') {
    return { title: "Kubit's own host cannot reach the LAN", steps: [
      { text: 'Check the link and address of the machine running kubit.' },
      { text: 'macOS: allow the app that started kubit under System Settings → Privacy & Security → Local Network.' },
      { text: 'Restart kubit serve from a terminal.' },
    ] }
  }
  const nodes = `/clusters/${c.cluster}/nodes`
  const node = c.nodeHref ? { label: `Open ${c.node}`, href: c.nodeHref + '#actions' } : { label: 'Nodes', href: nodes }
  const backups = { label: 'Backups', href: `/clusters/${c.cluster}/backups` }
  const addons = { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` }
  const network = { label: 'Network', href: `/clusters/${c.cluster}/network` }
  const workloads = { label: 'Workloads', href: `/clusters/${c.cluster}/workloads` }
  switch (kind) {
    case 'talos.unreachable':
      return { title: 'Machine not answering on the Talos API', steps: [
        { text: 'Check power and link; wake it if Wake-on-LAN is on.', link: { label: 'Inventory', href: '/fleet/inventory' } },
        { text: 'Moved to a new DHCP address: use Update address.', link: node },
        { text: 'Gone for good: remove it from the cluster and adopt a replacement.', link: { label: 'Remove node', href: nodes } },
      ] }
    case 'node.notready':
      return { title: 'Kubelet reports NotReady', steps: [
        { text: 'Check the conditions on the Kubernetes tab and kubelet health on the Services tab.', link: node },
        { text: 'Drain and reboot the node.', link: node },
        { text: 'Disk pressure: free space under /var or move workloads.' },
      ] }
    case 'node.memory-small':
      return { title: 'Node too small for the platform add-ons', steps: [
        { text: 'Give the machine at least 2 GiB; resize a lab VM on its host.', link: node },
        { text: 'Or replace it with a larger machine.', link: { label: 'Nodes', href: nodes } },
      ] }
    case 'api.unreachable':
      return { title: 'Kubernetes API unreachable', steps: [
        { text: 'Check whether the control planes answer on the Talos API.', link: { label: 'Nodes', href: nodes } },
        { text: 'etcd quorum lost for good: restore from a snapshot.', link: backups },
        { text: 'Only the VIP failing: reboot one control plane.' },
      ] }
    case 'etcd.unhealthy':
      return { title: 'etcd has lost health', steps: [
        { text: 'Bring back the down control planes.', link: { label: 'Nodes', href: nodes } },
        { text: 'Members lost for good with a majority up: remove them from the cluster.', link: { label: 'Remove node', href: nodes } },
        { text: 'Majority lost for good: restore the latest verified snapshot.', link: backups },
      ] }
    case 'etcd.members':
      return { title: 'etcd membership changed', steps: [
        { text: 'Every control plane should be a member.', link: { label: 'Nodes', href: nodes } },
      ] }
    case 'lb.lost':
      return { title: 'Ingress lost its LoadBalancer address', steps: [
        { text: 'Check the metallb-system workloads and the pool.', link: network },
        { text: 'Plan and apply the add-ons.', link: addons },
      ] }
    case 'lb.pool-exhausted':
      return { title: 'MetalLB pool exhausted', steps: [
        { text: 'Delete services that no longer need an address.', link: network },
        { text: 'Or widen the MetalLB range, then plan and apply.', link: addons },
      ] }
    case 'machine.ip-changed':
      return { title: 'Machine moved to a new address', steps: [
        { text: 'Use Update address, or pin a static address.', link: { label: 'Nodes', href: nodes } },
      ] }
    case 'backup.stale':
      return { title: 'etcd snapshots are behind schedule', steps: [
        { text: 'Read the last snapshot operation, then take one by hand.', link: backups },
        { text: 'Install the daemon as a service: kubit service install.' },
      ] }
    case 'offsite.failed':
      return { title: 'Off-site copy failed', steps: [
        { text: 'Test the target and fix the mount or credentials.', link: { label: 'Off-site', href: '/settings/offsite' } },
      ] }
    case 'cert.expiring':
      return { title: 'Credential expiring', steps: [
        { text: 'Rotate it, then download exported copies again.', link: { label: 'Lifecycle', href: `/clusters/${c.cluster}/lifecycle` } },
      ] }
    case 'workload.unavailable':
      return { title: 'Workload below desired replicas', steps: [
        { text: 'Open its pods; logs and events are in the pod dialog.', link: workloads },
        { text: 'Pending pods: check node capacity and taints.' },
      ] }
    case 'pod.crashloop':
      return { title: 'Pod crashlooping', steps: [
        { text: 'Read the pod log and events.', link: workloads },
        { text: 'Fix it in the deployment tooling.' },
      ] }
    case 'flux.not-ready':
      return { title: 'Flux sync failing', steps: [
        { text: 'Read the error on the Flux card.', link: addons },
        { text: 'Fix it in the apps repository and push.' },
      ] }
    case 'pvc.pending':
      return { title: 'PersistentVolumeClaim stuck Pending', steps: [
        { text: "Check the storage classes and the claim's class.", link: { label: 'Storage', href: `/clusters/${c.cluster}/storage` } },
        { text: 'Enable Longhorn or check its pods.', link: addons },
      ] }
    case 'service.no-endpoints':
      return { title: 'Service without endpoints', steps: [
        { text: "Compare the selector with the pods' labels and readiness.", link: network },
      ] }
    case 'ingress.no-address':
      return { title: 'Ingress has no address', steps: [
        { text: 'Check the ingress-nginx add-on and its address.', link: addons },
      ] }
    default:
      return null
  }
}

function labRunbook(kind: string, mac: string): Runbook | null {
  const host = { label: 'Lab host', href: `/labhosts/${mac}/overview` }
  switch (kind) {
    case 'labhost.disk-low':
      return { title: 'The VM disk is filling up', steps: [
        { text: 'Delete unused VMs.', link: host },
        { text: 'Move images and volumes off the lab VMs.' },
        { text: 'Re-provision a VM to reclaim its space, or rebuild the host with a larger disk.', link: host },
      ] }
    case 'labhost.memory-pressure':
      return { title: 'The host is short of memory', steps: [
        { text: 'Stop or shrink VMs to stay under host memory minus 2 GiB.', link: host },
      ] }
    case 'labhost.unreachable':
      return { title: 'Lab host not answering on SSH', steps: [
        { text: 'Check power and link, or power it on through remote management.', link: { label: 'Remote management', href: `/labhosts/${mac}/actions` } },
        { text: 'If it was updating, wait for the operation.', link: { label: 'Activity', href: '/operations' } },
      ] }
    case 'labhost.updates':
      return { title: 'Host updates pending', steps: [
        { text: 'Run Update host inside the maintenance window.', link: host },
      ] }
    default:
      return null
  }
}
