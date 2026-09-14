resource "helm_release" "argocd" {
  count = var.argocd.enabled ? 1 : 0

  name             = "argocd"
  namespace        = "argocd"
  create_namespace = true
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-cd"
  version          = var.chart_versions.argocd
  values           = length(var.argocd.values) > 0 ? [yamlencode(var.argocd.values)] : []
  wait             = true
  timeout          = 900

  set = [
    { name = "server.service.type", value = var.metallb.enabled ? "LoadBalancer" : "NodePort" },
    # TLS termination happens at the ingress or not at all on a LAN; keep the UI plain HTTP.
    { name = "configs.params.server\\.insecure", value = "true" },
  ]
  depends_on = [kubectl_manifest.metallb_l2]
}

data "kubernetes_secret_v1" "argocd_admin" {
  count = var.argocd.enabled ? 1 : 0

  metadata {
    name      = "argocd-initial-admin-secret"
    namespace = "argocd"
  }
  depends_on = [helm_release.argocd]
}

data "kubernetes_service_v1" "argocd" {
  count = var.argocd.enabled ? 1 : 0

  metadata {
    name      = "argocd-server"
    namespace = "argocd"
  }
  depends_on = [helm_release.argocd]
}
