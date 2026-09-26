resource "kubernetes_namespace_v1" "metallb" {
  count = var.metallb.enabled ? 1 : 0

  metadata {
    name = "metallb-system"
    labels = {
      "pod-security.kubernetes.io/enforce" = "privileged"
      "pod-security.kubernetes.io/audit"   = "privileged"
      "pod-security.kubernetes.io/warn"    = "privileged"
    }
  }
}

resource "helm_release" "metallb" {
  count = var.metallb.enabled ? 1 : 0

  name       = "metallb"
  namespace  = kubernetes_namespace_v1.metallb[0].metadata[0].name
  repository = "https://metallb.github.io/metallb"
  chart      = "metallb"
  version    = var.chart_versions.metallb
  values     = length(var.metallb.values) > 0 ? [yamlencode(var.metallb.values)] : []
  wait       = true
  atomic     = true
  timeout    = 600
}

resource "kubectl_manifest" "metallb_pool" {
  count = var.metallb.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "metallb.io/v1beta1"
    kind       = "IPAddressPool"
    metadata   = { name = "default", namespace = "metallb-system" }
    spec       = { addresses = [var.metallb.range] }
  })
  depends_on = [helm_release.metallb]
}

resource "kubectl_manifest" "metallb_l2" {
  count = var.metallb.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "metallb.io/v1beta1"
    kind       = "L2Advertisement"
    metadata   = { name = "default", namespace = "metallb-system" }
    spec       = {}
  })
  depends_on = [kubectl_manifest.metallb_pool]
}
