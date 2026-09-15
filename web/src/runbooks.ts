// What to do when an alert fires. Each step is an instruction; a link points at the
// place in Kubit where the action lives. Written for the admin reading it at 2 a.m.

export interface RunbookStep { text: string; link?: { label: string; href: string } }
export interface Runbook { title: string; why: string; steps: RunbookStep[] }

interface Ctx { cluster: string; node?: string; nodeHref?: string }

export function runbookFor(kind: string, c: Ctx): Runbook | null {
  if (c.cluster.startsWith('labhost:')) return labRunbook(kind, c.cluster.slice('labhost:'.length))
  const nodes = `/clusters/${c.cluster}/nodes`
  const node = c.nodeHref ? { label: `Open ${c.node}`, href: c.nodeHref + '#actions' } : { label: 'Nodes', href: nodes }
  switch (kind) {
    case 'talos.unreachable':
      return { title: 'Machine not answering on the Talos API', why: 'Kubit cannot reach port 50000. The machine is off, has no network, or is mid-reboot. Kubernetes may still schedule around it.', steps: [
        { text: 'Check power and link. If Wake-on-LAN is enabled for the machine, send a magic packet.', link: { label: 'Inventory', href: '/fleet/inventory' } },
        { text: 'If it moved to a new DHCP address, discovery will report it as "seen at"; use Update address.', link: node },
        { text: 'If the machine is gone for good: remove it from the cluster so etcd/quorum expectations match reality, then adopt a replacement.', link: { label: 'Remove node', href: nodes } },
      ] }
    case 'node.notready':
      return { title: 'Kubelet reports NotReady', why: 'The node is reachable but its kubelet is not posting a healthy status: pressure conditions, a broken CNI, or a kubelet restart in progress.', steps: [
        { text: 'Open the node: Kubernetes tab shows the conditions (DiskPressure, MemoryPressure, PIDPressure) and Services tab shows kubelet/containerd health.', link: node },
        { text: 'A reboot through Kubit (drain first) fixes most transient cases.', link: node },
        { text: 'Disk pressure: free space under /var (image garbage collection runs on its own once below the threshold) or move workloads.' },
      ] }
    case 'api.unreachable':
      return { title: 'Kubernetes API unreachable', why: 'Kubit cannot talk to the endpoint (VIP or first control plane). Either every control plane is down, the VIP is not being announced, or the network between this host and the cluster is broken.', steps: [
        { text: 'Check the control planes\' Talos reachability on the Nodes page; if they answer, the API server pods or etcd are the problem.', link: { label: 'Nodes', href: nodes } },
        { text: 'etcd unhealthy at the same time: see that runbook. If quorum is lost for good, restore from a snapshot.', link: { label: 'Backups', href: `/clusters/${c.cluster}/backups` } },
        { text: 'Only the VIP failing: reboot one control plane; Talos re-elects the VIP holder.' },
      ] }
    case 'etcd.unhealthy':
      return { title: 'etcd has lost health', why: 'Fewer than a majority of members answer, or a member reports alarms (NOSPACE). Without quorum the API server stops accepting writes.', steps: [
        { text: 'Bring back the down control planes first (power, network, reboot). Quorum returns on its own once a majority is up.', link: { label: 'Nodes', href: nodes } },
        { text: 'Members permanently lost with a majority still up: remove them from the cluster so the expected count drops.', link: { label: 'Remove node', href: nodes } },
        { text: 'Majority lost permanently: restore the latest verified snapshot onto the surviving control plane (Disaster recovery).', link: { label: 'Backups', href: `/clusters/${c.cluster}/backups` } },
      ] }
    case 'etcd.members':
      return { title: 'etcd membership changed', why: 'The member count differs from the last observation. Expected after add/remove operations; unexpected otherwise.', steps: [
        { text: 'Compare with the control planes on the Nodes page; every control plane should be a member.', link: { label: 'Nodes', href: nodes } },
      ] }
    case 'lb.lost':
      return { title: 'Ingress lost its LoadBalancer address', why: 'MetalLB stopped announcing the address: the speaker pods are down, the pool changed, or the service was deleted.', steps: [
        { text: 'Check the metallb-system workloads and the pool map.', link: { label: 'Network', href: `/clusters/${c.cluster}/network` } },
        { text: 'Plan and apply the platform layer to reconcile the pool and the ingress service.', link: { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` } },
      ] }
    case 'lb.pool-exhausted':
      return { title: 'MetalLB pool exhausted', why: 'Every address in the range is allocated; new LoadBalancer services stay Pending.', steps: [
        { text: 'See who holds each address and delete services that no longer need one.', link: { label: 'Network', href: `/clusters/${c.cluster}/network` } },
        { text: 'Or widen the range under Add-ons → MetalLB, then Plan/Apply.', link: { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` } },
      ] }
    case 'machine.ip-changed':
      return { title: 'Machine moved to a new address', why: 'Discovery saw this MAC at a different IP than the declaration. Kubit still talks to the old one.', steps: [
        { text: 'Use Update address on the Nodes page (or pin a static address so it cannot move again).', link: { label: 'Nodes', href: nodes } },
      ] }
    case 'backup.stale':
      return { title: 'etcd snapshots are behind schedule', why: 'The scheduled snapshot did not run: the cluster was not ready/idle, or the snapshot operation failed.', steps: [
        { text: 'Look at the last etcd snapshot operation for the error, then take one by hand.', link: { label: 'Backups', href: `/clusters/${c.cluster}/backups` } },
        { text: 'If the daemon was simply not running, consider installing it as a service (kubit service install).' },
      ] }
    case 'offsite.failed':
      return { title: 'Off-site copy failed', why: 'The local snapshot/backup exists but could not be written to the off-site target (share unmounted, credentials, bucket policy).', steps: [
        { text: 'Test the target and fix the mount or credentials; the next snapshot copies again.', link: { label: 'Kubit settings', href: '/settings' } },
      ] }
    case 'cert.expiring':
      return { title: 'Credential expiring', why: 'The talosconfig or kubeconfig Kubit uses (and hands out via Download) is close to its end date. Past it, Kubit and every exported kubeconfig lose access.', steps: [
        { text: 'Rotate the credential; exported copies must be re-downloaded afterwards.', link: { label: 'Settings → Credentials', href: `/clusters/${c.cluster}/settings` } },
      ] }
    case 'workload.unavailable':
      return { title: 'Workload below desired replicas', why: 'Pods are not becoming Ready: failing probes, image pull errors, missing resources, or a node that cannot schedule them.', steps: [
        { text: 'Open the workload\'s pods: phase and restarts point at the cause; the pod dialog has logs and events.', link: { label: 'Workloads', href: `/clusters/${c.cluster}/workloads` } },
        { text: 'Pending pods with no node: check node capacity and taints (a pool taint needs a matching toleration).' },
      ] }
    case 'pod.crashloop':
      return { title: 'Pod crashlooping', why: 'The container exits shortly after start; Kubernetes backs off between restarts.', steps: [
        { text: 'Read the last log lines and events of the pod (usually a config, secret or dependency error).', link: { label: 'Workloads', href: `/clusters/${c.cluster}/workloads` } },
        { text: 'Fix it in the deployment tooling (Argo CD / Helm); Kubit does not edit application manifests.' },
      ] }
    case 'pvc.pending':
      return { title: 'PersistentVolumeClaim stuck Pending', why: 'No StorageClass provisioned a volume: the class does not exist, has no provisioner, or the provisioner is down.', steps: [
        { text: 'Check the storage classes and the claim\'s class name.', link: { label: 'Storage', href: `/clusters/${c.cluster}/storage` } },
        { text: 'Kubit ships no storage; install one (local-path-provisioner, Longhorn) through Argo CD or Helm and mark it default.' },
      ] }
    case 'service.no-endpoints':
      return { title: 'Service without endpoints', why: 'Its selector matches no Ready pod: labels differ, or the pods are not Ready.', steps: [
        { text: 'Compare the service selector with the pods\' labels; check pod readiness.', link: { label: 'Network', href: `/clusters/${c.cluster}/network` } },
      ] }
    case 'ingress.no-address':
      return { title: 'Ingress has no address', why: 'No ingress controller claimed it: wrong ingressClassName, or ingress-nginx is not running / has no LoadBalancer IP.', steps: [
        { text: 'Check the ingress-nginx add-on state and its LoadBalancer address.', link: { label: 'Add-ons', href: `/clusters/${c.cluster}/addons` } },
      ] }
    default:
      return null
  }
}

function labRunbook(kind: string, mac: string): Runbook | null {
  const host = { label: 'Lab host', href: `/machines/${mac}#labhost` }
  switch (kind) {
    case 'labhost.disk-low':
      return { title: 'The VM disk is filling up', why: 'VM disks are thin-provisioned and grow as Talos writes. When the host filesystem is full, every VM pauses at once.', steps: [
        { text: 'Delete VMs you no longer use; their disks are freed immediately.', link: host },
        { text: 'Move workload data off the lab: images and PersistentVolumes on the VMs are what grows.' },
        { text: 'Re-provision a VM to reclaim its space (its Talos install is wiped), or rebuild the host with a larger disk.', link: host },
      ] }
    case 'labhost.memory-pressure':
      return { title: 'The host is short of memory', why: 'The VMs and the host together use nearly all RAM; the kernel will swap or kill a VM next.', steps: [
        { text: 'Stop a VM that is not needed, or resize VMs so their total stays under the host memory minus 2 GiB.', link: host },
      ] }
    case 'labhost.unreachable':
      return { title: 'Lab host not answering on SSH', why: 'Three checks in a row found no SSH. The host is off, rebooting, or cut off; its VMs and their cluster are down with it.', steps: [
        { text: 'Check power and link. Power it on or reset it through remote management.', link: { label: 'Remote management', href: `/machines/${mac}#oob` } },
        { text: 'If it was updating, wait: the Update host operation reboots the host and reports in Activity.', link: { label: 'Activity', href: '/operations' } },
      ] }
    case 'labhost.updates':
      return { title: 'Host updates pending', why: 'Security fixes install on their own daily; a kernel or a large upgrade waits for Update host, which parks the VMs first.', steps: [
        { text: 'Run Update host from the Lab host tab, inside the cluster\'s maintenance window if one is set.', link: host },
      ] }
    default:
      return null
  }
}
