resource "kubectl_manifest" "builds_namespace" {
  count = var.builds.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = "kubit-builds"
      labels = {
        "pod-security.kubernetes.io/enforce" = "privileged"
        "pod-security.kubernetes.io/audit"   = "privileged"
        "pod-security.kubernetes.io/warn"    = "privileged"
      }
    }
  })
}

resource "kubectl_manifest" "builds_pool" {
  count = var.builds.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "metallb.io/v1beta1"
    kind       = "IPAddressPool"
    metadata   = { name = "builds", namespace = "metallb-system" }
    spec       = { addresses = ["${var.builds.ip}/32"], autoAssign = false }
  })
  depends_on = [kubectl_manifest.metallb_l2]
}

resource "kubectl_manifest" "builds_registry_volume" {
  count = var.builds.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "v1"
    kind       = "PersistentVolumeClaim"
    metadata   = { name = "registry", namespace = "kubit-builds" }
    spec = {
      accessModes      = ["ReadWriteOnce"]
      storageClassName = "longhorn"
      resources        = { requests = { storage = var.builds.registry_size } }
    }
  })
  depends_on = [kubectl_manifest.builds_namespace, helm_release.longhorn]
}

resource "kubectl_manifest" "builds_registry" {
  count = var.builds.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata   = { name = "registry", namespace = "kubit-builds", labels = { "app.kubernetes.io/name" = "registry" } }
    spec = {
      replicas = 1
      strategy = { type = "Recreate" }
      selector = { matchLabels = { "app.kubernetes.io/name" = "registry" } }
      template = {
        metadata = { labels = { "app.kubernetes.io/name" = "registry" } }
        spec = {
          containers = [{
            name  = "registry"
            image = "registry:3"
            env = [
              { name = "REGISTRY_HTTP_ADDR", value = ":5000" },
              { name = "REGISTRY_STORAGE_DELETE_ENABLED", value = "true" },
            ]
            ports          = [{ name = "registry", containerPort = 5000 }]
            readinessProbe = { httpGet = { path = "/v2/", port = "registry" } }
            resources      = { requests = { cpu = "20m", memory = "32Mi" }, limits = { memory = "256Mi" } }
            volumeMounts   = [{ name = "data", mountPath = "/var/lib/registry" }]
          }]
          volumes = [{ name = "data", persistentVolumeClaim = { claimName = "registry" } }]
        }
      }
    }
  })
  depends_on = [kubectl_manifest.builds_registry_volume]
}

resource "kubectl_manifest" "builds_registry_service" {
  count = var.builds.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "v1"
    kind       = "Service"
    metadata = {
      name      = "registry"
      namespace = "kubit-builds"
      annotations = {
        "metallb.io/address-pool"     = "builds"
        "metallb.io/loadBalancerIPs" = var.builds.ip
      }
    }
    spec = {
      type     = "LoadBalancer"
      selector = { "app.kubernetes.io/name" = "registry" }
      ports    = [{ name = "registry", port = 5000, targetPort = "registry" }]
    }
  })
  depends_on = [kubectl_manifest.builds_pool, kubectl_manifest.builds_namespace]
}

resource "kubectl_manifest" "builds_buildkitd_config" {
  count = var.builds.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata   = { name = "buildkitd", namespace = "kubit-builds" }
    data = {
      "buildkitd.toml" = <<-TOML
        [worker.oci]
          maxUsedSpace = "8GB"
        [registry."registry.kubit-builds.svc:5000"]
          http = true
          insecure = true
        [registry."registry.kubit"]
          mirrors = ["registry.kubit-builds.svc:5000"]
      TOML
    }
  })
  depends_on = [kubectl_manifest.builds_namespace]
}

resource "kubectl_manifest" "builds_buildkitd" {
  count = var.builds.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata   = { name = "buildkitd", namespace = "kubit-builds", labels = { "app.kubernetes.io/name" = "buildkitd" } }
    spec = {
      replicas = 1
      selector = { matchLabels = { "app.kubernetes.io/name" = "buildkitd" } }
      template = {
        metadata = {
          labels      = { "app.kubernetes.io/name" = "buildkitd" }
          annotations = { "kubit.dev/config" = sha1(kubectl_manifest.builds_buildkitd_config[0].yaml_body) }
        }
        spec = {
          containers = [{
            name            = "buildkitd"
            image           = "moby/buildkit:v0.25.1"
            args            = ["--addr", "tcp://0.0.0.0:1234", "--addr", "unix:///run/buildkit/buildkitd.sock", "--config", "/etc/buildkit/buildkitd.toml"]
            ports           = [{ name = "buildkitd", containerPort = 1234 }]
            securityContext = { privileged = true }
            readinessProbe  = { tcpSocket = { port = "buildkitd" } }
            resources       = { requests = { cpu = "50m", memory = "128Mi" }, limits = { memory = "1536Mi" } }
            volumeMounts = [
              { name = "config", mountPath = "/etc/buildkit" },
              { name = "cache", mountPath = "/var/lib/buildkit" },
            ]
          }]
          volumes = [
            { name = "config", configMap = { name = "buildkitd" } },
            { name = "cache", emptyDir = { sizeLimit = "10Gi" } },
          ]
        }
      }
    }
  })
  depends_on = [kubectl_manifest.builds_buildkitd_config]
}

resource "kubectl_manifest" "builds_buildkitd_service" {
  count = var.builds.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "v1"
    kind       = "Service"
    metadata   = { name = "buildkitd", namespace = "kubit-builds" }
    spec = {
      selector = { "app.kubernetes.io/name" = "buildkitd" }
      ports    = [{ name = "buildkitd", port = 1234, targetPort = "buildkitd" }]
    }
  })
  depends_on = [kubectl_manifest.builds_namespace]
}
