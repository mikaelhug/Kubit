# Talos enforces the "baseline" Pod Security level cluster-wide; the speaker needs host
# networking and NET_RAW, so its namespace must opt out.
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
  timeout    = 600
}

# The CRs go through the kubectl provider: it does not need the CRDs to exist at plan
# time, which is what breaks kubernetes_manifest on a first apply.
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
    spec       = { ipAddressPools = ["default"] }
  })
  depends_on = [kubectl_manifest.metallb_pool]
}
