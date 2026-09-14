resource "helm_release" "cert_manager" {
  count = var.cert_manager.enabled ? 1 : 0

  name             = "cert-manager"
  namespace        = "cert-manager"
  create_namespace = true
  repository       = "https://charts.jetstack.io"
  chart            = "cert-manager"
  version          = var.chart_versions.cert_manager
  values           = length(var.cert_manager.values) > 0 ? [yamlencode(var.cert_manager.values)] : []
  wait             = true
  timeout          = 600

  set = [
    { name = "crds.enabled", value = "true" },
  ]
}
