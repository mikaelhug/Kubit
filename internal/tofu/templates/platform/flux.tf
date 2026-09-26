resource "helm_release" "flux" {
  count = var.flux.enabled ? 1 : 0

  name             = "flux"
  namespace        = "flux-system"
  create_namespace = true
  repository       = "https://fluxcd-community.github.io/helm-charts"
  chart            = "flux2"
  version          = var.chart_versions.flux
  values           = length(var.flux.values) > 0 ? [yamlencode(var.flux.values)] : []
  wait             = true
  atomic           = true
  timeout          = 600

  set = [
    { name = "imageAutomationController.create", value = "false" },
    { name = "imageReflectionController.create", value = "false" },
    { name = "crds.annotations.helm\\.sh/resource-policy", value = "keep" },
  ]
}

resource "kubectl_manifest" "flux_source" {
  count = var.flux.enabled && var.flux.repository != null ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "source.toolkit.fluxcd.io/v1"
    kind       = "GitRepository"
    metadata   = { name = "flux-system", namespace = "flux-system" }
    spec = {
      interval = var.flux.repository.interval
      url      = var.flux.repository.url
      ref      = { branch = var.flux.repository.branch }
    }
  })
  depends_on = [
    helm_release.flux,
    helm_release.metallb,
    kubectl_manifest.metallb_pool,
    kubectl_manifest.metallb_l2,
    helm_release.ingress_nginx,
    helm_release.metrics_server,
    helm_release.cert_manager,
    helm_release.longhorn,
    kubectl_manifest.runtimeclass_gvisor,
    kubectl_manifest.runtimeclass_gvisor_kvm,
  ]
}

resource "kubectl_manifest" "flux_sync" {
  count = var.flux.enabled && var.flux.repository != null ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "kustomize.toolkit.fluxcd.io/v1"
    kind       = "Kustomization"
    metadata   = { name = "flux-system", namespace = "flux-system" }
    spec = {
      interval       = "10m"
      path           = var.flux.repository.path
      prune          = true
      deletionPolicy = "Orphan"
      sourceRef      = { kind = "GitRepository", name = "flux-system" }
      decryption     = { provider = "sops", secretRef = { name = "sops-age" } }
    }
  })
  depends_on = [kubectl_manifest.flux_source]
}
