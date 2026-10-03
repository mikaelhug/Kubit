import { splitList, type ClusterForm, type ClusterOIDC, type ClusterSpec } from './api'

export function formOf(spec: ClusterSpec['spec']): ClusterForm {
  return {
    talosVersion: spec.talosVersion, kubernetesVersion: spec.kubernetesVersion, endpoint: spec.controlPlane.endpoint, vip: spec.controlPlane.vip ?? '', allowScheduling: spec.controlPlane.allowScheduling ?? null,
    podCIDR: spec.network.podCIDR, serviceCIDR: spec.network.serviceCIDR, extensions: spec.extensions ?? [], nameservers: spec.network.nameservers ?? [], ntp: spec.network.ntp ?? [],
    networkPolicies: spec.network.policies ?? true, discovery: spec.network.discovery ?? true, firewall: spec.network.firewall ?? false,
    etcdSnapshotInterval: spec.backup?.etcd.interval ?? '6h', etcdSnapshotKeep: spec.backup?.etcd.keep ?? 28, maintenanceWindow: spec.maintenance?.window ?? '', maintenanceTimezone: spec.maintenance?.timezone ?? '', oidc: spec.auth?.oidc ?? null,
  }
}

type EditableForm = Omit<ClusterForm, 'extensions' | 'nameservers' | 'ntp' | 'etcdSnapshotKeep' | 'oidc'> & { extensions: string; nameservers: string; ntp: string; etcdSnapshotKeep: string; oidc: ClusterOIDC }

const noOIDC: ClusterOIDC = { issuer: '', clientID: '', usernameClaim: 'preferred_username', groupsClaim: 'groups', adminGroup: '' }

export function editableForm(spec: ClusterSpec['spec']): EditableForm {
  const f = formOf(spec)
  return { ...f, extensions: f.extensions.join(', '), nameservers: f.nameservers.join(', '), ntp: f.ntp.join(', '), etcdSnapshotKeep: String(f.etcdSnapshotKeep), oidc: f.oidc ?? noOIDC }
}

export function submittedForm(f: EditableForm): ClusterForm {
  return { ...f, extensions: splitList(f.extensions), nameservers: splitList(f.nameservers), ntp: splitList(f.ntp), etcdSnapshotKeep: Number(f.etcdSnapshotKeep) || 0 }
}
