resource "helm_release" "ingress_nginx" {
  count = var.ingress_nginx.enabled ? 1 : 0

  name             = "ingress-nginx"
  namespace        = "ingress-nginx"
  create_namespace = true
  repository       = "https://kubernetes.github.io/ingress-nginx"
  chart            = "ingress-nginx"
  version          = var.chart_versions.ingress_nginx
  wait             = true
  timeout          = 600

  set = [
    { name = "controller.service.type", value = var.metallb.enabled ? "LoadBalancer" : "NodePort" },
    { name = "controller.ingressClassResource.default", value = "true" },
  ]
  depends_on = [kubectl_manifest.metallb_l2]
}

data "kubernetes_service_v1" "ingress_nginx" {
  count = var.ingress_nginx.enabled ? 1 : 0

  metadata {
    name      = "ingress-nginx-controller"
    namespace = "ingress-nginx"
  }
  depends_on = [helm_release.ingress_nginx]
}
