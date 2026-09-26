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
