data "kubectl_file_documents" "traefik_gateway_api" {
  count = var.traefik.enabled ? 1 : 0

  content = file("${path.module}/gateway-api-${var.chart_versions.gateway_api}.yaml")
}

resource "kubectl_manifest" "traefik_gateway_api" {
  for_each = try(data.kubectl_file_documents.traefik_gateway_api[0].manifests, {})

  yaml_body         = each.value
  server_side_apply = true
  force_conflicts   = true
  apply_only        = true
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
  depends_on = [kubectl_manifest.metallb_l2, kubectl_manifest.traefik_gateway_api]
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
