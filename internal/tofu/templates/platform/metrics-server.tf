resource "helm_release" "metrics_server" {
  count = var.metrics_server.enabled ? 1 : 0

  name             = "metrics-server"
  namespace        = "kube-system"
  repository       = "https://kubernetes-sigs.github.io/metrics-server"
  chart            = "metrics-server"
  version          = var.chart_versions.metrics_server
  values           = length(var.metrics_server.values) > 0 ? [yamlencode(var.metrics_server.values)] : []
  wait             = true
  atomic           = true
  timeout          = 600

  # Talos kubelets serve self-signed certificates unless a serving-cert approver is
  # installed; metrics-server must skip verification to scrape them.
  set = [
    { name = "args[0]", value = "--kubelet-insecure-tls" },
  ]
}
