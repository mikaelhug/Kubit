interface RunbookStep { text: string; link?: { label: string; href: string } }
export interface Runbook { title: string; why: string; steps: RunbookStep[] }

interface Ctx { cluster: string; node?: string; nodeHref?: string }

export function runbookFor(kind: string, c: Ctx): Runbook | null {
  if (c.cluster.startsWith('labhost:')) return labRunbook(kind, c.cluster.slice('labhost:'.length))
  if (kind === 'observer.offline') {
    return { title: "Kubit's own host cannot reach the LAN", why: 'Probes and the default gateway fail with no route; alerts pause until it sees again.', steps: [
      { text: 'Check the link and address of the machine running kubit.' },
      { text: 'macOS: System Settings → Privacy & Security → Local Network must allow the app that started kubit; a daemon started from a terminal inherits that terminal\'s permission.' },
      { text: 'Restart kubit serve from a terminal; a process that lost its network permission does not get it back on its own.' },
    ] }
  }
  const nodes = `/clusters/${c.cluster}/nodes`
  const node = c.nodeHref ? { label: `Open ${c.node}`, href: c.nodeHref + '#actions' } : { label: 'Nodes', href: nodes }
  switch (kind) {
    case 'talos.unreachable':
      return { title: 'Machine not answering on the Talos API', why: 'Port 50000 does not answer: off, no network, or rebooting.', steps: [
        { text: 'Check power and link. If Wake-on-LAN is enabled for the machine, send a magic packet.', link: { label: 'Inventory', href: '/fleet/inventory' } },
        { text: 'If it moved to a new DHCP address, discovery will report it as "seen at"; use Update address.', link: node },
        { text: 'If the machine is gone for good: remove it from the cluster so etcd/quorum expectations match reality, then adopt a replacement.', link: { label: 'Remove node', href: nodes } },
      ] }
    case 'node.notready':
      return { title: 'Kubelet reports NotReady', why: 'Reachable, but the kubelet reports pressure, a broken CNI or a restart.', steps: [
        { text: 'Open the node: Kubernetes tab shows the conditions (DiskPressure, MemoryPressure, PIDPressure) and Services tab shows kubelet/containerd health.', link: node },
        { text: 'A reboot through Kubit (drain first) fixes most transient cases.', link: node },
        { text: 'Disk pressure: free space under /var (image garbage collection runs on its own once below the threshold) or move workloads.' },
      ] }
    case 'node.memory-small':
      return { title: 'Node too small for the platform add-ons', why: 'Under 768 MiB allocatable the add-ons are OOM-killed in a loop.', steps: [
        { text: 'Give the machine at least 2 GiB. A lab VM is resized on its host\'s Lab host tab (the VM restarts).', link: node },
        { text: 'Or remove the node from the cluster and adopt a larger machine.', link: { label: 'Nodes', href: nodes } },
      ] }
    case 'api.unreachable':
      return { title: 'Kubernetes API unreachable', why: 'The endpoint does not answer: control planes down, VIP not announced, or no route.', steps: [
        { text: 'Check the control planes\' Talos reachability on the Nodes page; if they answer, the API server pods or etcd are the problem.', link: { label: 'Nodes', href: nodes } },
        { text: 'etcd unhealthy at the same time: see that runbook. If quorum is lost for good, restore from a snapshot.', link: { label: 'Backups', href: `/clusters/${c.cluster}/backups` } },
        { text: 'Only the VIP failing: reboot one control plane; Talos re-elects the VIP holder.' },
      ] }
    case 'etcd.unhealthy':
      return { title: 'etcd has lost health', why: 'No majority answers or a member reports alarms; without quorum writes stop.', steps: [
        { text: 'Bring back the down control planes first (power, network, reboot). Quorum returns on its own once a majority is up.', link: { label: 'Nodes', href: nodes } },
        { text: 'Members permanently lost with a majority still up: remove them from the cluster so the expected count drops.', link: { label: 'Remove node', href: nodes } },
        { text: 'Majority lost permanently: restore the latest verified snapshot onto the surviving control plane (Disaster recovery).', link: { label: 'Backups', href: `/clusters/${c.cluster}/backups` } },
      ] }
    case 'etcd.members':
      return { title: 'etcd membership changed', why: 'The member count changed since the last observation.', steps: [
        { text: 'Compare with the control planes on the Nodes page; every control plane should be a member.', link: { label: 'Nodes', href: nodes } },
      ] }
    case 'lb.lost':
      return { title: 'Ingress lost its LoadBalancer address', why: 'MetalLB stopped announcing it: speakers down, pool changed, or service deleted.', steps: [
        { text: 'Check the metallb-system workloads and the pool map.', link: { label: 'Network', href: `/clusters/${c.cluster}/network` } },
        { text: 'Plan and apply the platform layer to reconcile the pool and the ingress service.', link: { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` } },
      ] }
    case 'lb.pool-exhausted':
      return { title: 'MetalLB pool exhausted', why: 'Every address is allocated; new LoadBalancer services stay Pending.', steps: [
        { text: 'See who holds each address and delete services that no longer need one.', link: { label: 'Network', href: `/clusters/${c.cluster}/network` } },
        { text: 'Or widen the range under Add-ons → MetalLB, then Plan/Apply.', link: { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` } },
      ] }
    case 'machine.ip-changed':
      return { title: 'Machine moved to a new address', why: 'Discovery saw this MAC at a different IP than declared.', steps: [
        { text: 'Use Update address on the Nodes page (or pin a static address so it cannot move again).', link: { label: 'Nodes', href: nodes } },
      ] }
    case 'backup.stale':
      return { title: 'etcd snapshots are behind schedule', why: 'The scheduled snapshot did not run or failed.', steps: [
        { text: 'Look at the last etcd snapshot operation for the error, then take one by hand.', link: { label: 'Backups', href: `/clusters/${c.cluster}/backups` } },
        { text: 'If the daemon was simply not running, consider installing it as a service (kubit service install).' },
      ] }
    case 'offsite.failed':
      return { title: 'Off-site copy failed', why: 'The local copy exists but the off-site target refused it.', steps: [
        { text: 'Test the target and fix the mount or credentials; the next snapshot copies again.', link: { label: 'Kubit settings', href: '/settings' } },
      ] }
    case 'cert.expiring':
      return { title: 'Credential expiring', why: 'Past its end date Kubit and every exported kubeconfig lose access.', steps: [
        { text: 'Rotate the credential; exported copies must be re-downloaded afterwards.', link: { label: 'Lifecycle', href: `/clusters/${c.cluster}/lifecycle` } },
      ] }
    case 'workload.unavailable':
      return { title: 'Workload below desired replicas', why: 'Pods are not becoming Ready.', steps: [
        { text: 'Open the workload\'s pods: phase and restarts point at the cause; the pod dialog has logs and events.', link: { label: 'Workloads', href: `/clusters/${c.cluster}/workloads` } },
        { text: 'Pending pods with no node: check node capacity and taints (a pool taint needs a matching toleration).' },
      ] }
    case 'pod.crashloop':
      return { title: 'Pod crashlooping', why: 'The container exits shortly after start.', steps: [
        { text: 'Read the last log lines and events of the pod (usually a config, secret or dependency error).', link: { label: 'Workloads', href: `/clusters/${c.cluster}/workloads` } },
        { text: 'Fix it in the deployment tooling (Flux / Helm); Kubit does not edit application manifests.' },
      ] }
    case 'flux.not-ready':
      return { title: 'Flux sync failing', why: 'Flux cannot apply the repository; the last good revision stays.', steps: [
        { text: 'Read the error on the Flux card; it names the file or object.', link: { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` } },
        { text: 'Fix it in the apps repository and push; Flux retries on the next fetch.' },
      ] }
    case 'pvc.pending':
      return { title: 'PersistentVolumeClaim stuck Pending', why: 'No StorageClass provisioned a volume.', steps: [
        { text: 'Check the storage classes and the claim\'s class name.', link: { label: 'Storage', href: `/clusters/${c.cluster}/storage` } },
        { text: 'Enable Longhorn, or check that its pods are ready.', link: { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` } },
      ] }
    case 'service.no-endpoints':
      return { title: 'Service without endpoints', why: 'Its selector matches no Ready pod.', steps: [
        { text: 'Compare the service selector with the pods\' labels; check pod readiness.', link: { label: 'Network', href: `/clusters/${c.cluster}/network` } },
      ] }
    case 'ingress.no-address':
      return { title: 'Ingress has no address', why: 'No ingress controller claimed it.', steps: [
        { text: 'Check the ingress-nginx add-on state and its LoadBalancer address.', link: { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` } },
      ] }
    default:
      return null
  }
}

function labRunbook(kind: string, mac: string): Runbook | null {
  const host = { label: 'Lab host', href: `/labhosts/${mac}/overview` }
  switch (kind) {
    case 'labhost.disk-low':
      return { title: 'The VM disk is filling up', why: 'When the host filesystem is full every VM pauses.', steps: [
        { text: 'Delete VMs you no longer use; their disks are freed immediately.', link: host },
        { text: 'Move workload data off the lab: images and PersistentVolumes on the VMs are what grows.' },
        { text: 'Re-provision a VM to reclaim its space (its Talos install is wiped), or rebuild the host with a larger disk.', link: host },
      ] }
    case 'labhost.memory-pressure':
      return { title: 'The host is short of memory', why: 'The VMs and the host use nearly all RAM.', steps: [
        { text: 'Stop a VM that is not needed, or resize VMs so their total stays under the host memory minus 2 GiB.', link: host },
      ] }
    case 'labhost.unreachable':
      return { title: 'Lab host not answering on SSH', why: 'Three checks found no SSH; its VMs are down with it.', steps: [
        { text: 'Check power and link. Power it on or reset it through remote management.', link: { label: 'Remote management', href: `/labhosts/${mac}/actions` } },
        { text: 'If it was updating, wait: the Update host operation reboots the host and reports in Activity.', link: { label: 'Activity', href: '/operations' } },
      ] }
    case 'labhost.updates':
      return { title: 'Host updates pending', why: 'A kernel or large upgrade waits for Update host.', steps: [
        { text: 'Run Update host from the Lab host tab, inside the cluster\'s maintenance window if one is set.', link: host },
      ] }
    default:
      return null
  }
}
