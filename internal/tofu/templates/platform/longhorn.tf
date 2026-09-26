# Replicated block storage on the nodes' data disks. Kubit labels every node for
# Longhorn at machine-config time (create-default-disk: config + the disk list for
# nodes with data disks, false for the rest), so the chart only has to trust the labels.
# Longhorn's manager and engine run privileged; Talos enforces the baseline Pod
# Security level everywhere else, so the namespace carries its own exemption.
resource "kubectl_manifest" "longhorn_namespace" {
  count = var.longhorn.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = "longhorn-system"
      labels = {
        "pod-security.kubernetes.io/enforce" = "privileged"
        "pod-security.kubernetes.io/audit"   = "privileged"
        "pod-security.kubernetes.io/warn"    = "privileged"
      }
    }
  })
}

resource "helm_release" "longhorn" {
  count = var.longhorn.enabled ? 1 : 0

  name       = "longhorn"
  namespace  = "longhorn-system"
  repository = "https://charts.longhorn.io"
  chart      = "longhorn"
  version    = var.chart_versions.longhorn
  values     = length(var.longhorn.values) > 0 ? [yamlencode(var.longhorn.values)] : []
  wait       = true
  timeout    = 900

  set = [
    { name = "defaultSettings.createDefaultDiskLabeledNodes", value = "true" },
    { name = "defaultSettings.defaultDataPath", value = "/var/mnt/data-1" },
    { name = "defaultSettings.storageReservedPercentageForDefaultDisk", value = "5" },
    { name = "defaultSettings.defaultReplicaCount", value = tostring(var.longhorn.replicas) },
    { name = "persistence.defaultClass", value = "true" },
    { name = "persistence.defaultClassReplicaCount", value = tostring(var.longhorn.replicas) },
    { name = "longhornUI.replicas", value = "1" },
  ]
  depends_on = [kubectl_manifest.longhorn_namespace]
}
