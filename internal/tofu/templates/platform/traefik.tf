resource "helm_release" "gateway_api" {
  count = var.traefik.enabled ? 1 : 0

  name           = "gateway-api"
  namespace      = "kube-system"
  chart          = "${path.module}/charts/gateway-api"
  take_ownership = true
  wait           = true
  atomic         = true
  timeout        = 300
}

resource "helm_release" "traefik" {
  count = var.traefik.enabled ? 1 : 0

  name             = "traefik"
  namespace        = "traefik"
  create_namespace = true
  repository       = "https://traefik.github.io/charts"
  chart            = "traefik"
  version          = var.chart_versions.traefik
  values           = length(var.traefik.values) > 0 ? [yamlencode(var.traefik.values)] : []
  wait             = true
  atomic           = true
  timeout          = 600

  set = concat(
    [{ name = "service.spec.type", value = var.metallb.enabled ? "LoadBalancer" : "NodePort" }],
    var.ingress_ip_pin != "" ? [{ name = "service.annotations.metallb\\.io/loadBalancerIPs", value = var.ingress_ip_pin }] : [],
  )
  depends_on = [kubectl_manifest.metallb_l2, helm_release.gateway_api]
}

resource "kubectl_manifest" "traefik_nginx_class" {
  count = var.traefik.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "networking.k8s.io/v1"
    kind       = "IngressClass"
    metadata   = { name = "nginx" }
    spec       = { controller = "k8s.io/ingress-nginx" }
  })
  depends_on = [helm_release.traefik]
}

data "kubernetes_service_v1" "traefik" {
  count = var.traefik.enabled ? 1 : 0

  metadata {
    name      = "traefik"
    namespace = "traefik"
  }
  depends_on = [helm_release.traefik]
}
