locals {
  backup_env = concat(
    [
      { name = "AWS_REGION", value = var.backup.region },
      { name = "BUCKET", value = var.backup.bucket },
      { name = "CLUSTER_NAME", value = var.backup.cluster },
      { name = "S3_PREFIX", value = var.backup.prefix },
      { name = "AGE_X25519_PUBLIC_KEY", value = try(var.backup.age_recipients[0], "") },
      { name = "AGE_RECIPIENT_PUBLIC_KEY", value = join(",", var.backup.age_recipients) },
      { name = "USE_PATH_STYLE", value = tostring(var.backup.path_style) },
      { name = "ENABLE_COMPRESSION", value = tostring(var.backup.compression) },
    ],
    var.backup.endpoint == "" ? [] : [{ name = "CUSTOM_S3_ENDPOINT", value = var.backup.endpoint }],
  )
}

resource "kubectl_manifest" "backup_namespace" {
  count = var.backup.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = "talos-backup"
      labels = {
        "pod-security.kubernetes.io/enforce" = "restricted"
        "pod-security.kubernetes.io/audit"   = "restricted"
        "pod-security.kubernetes.io/warn"    = "restricted"
      }
    }
  })
}

resource "kubectl_manifest" "backup_account" {
  count = var.backup.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "talos.dev/v1alpha1"
    kind       = "ServiceAccount"
    metadata   = { name = "talos-backup-secrets", namespace = "talos-backup" }
    spec       = { roles = ["os:etcd:backup"] }
  })
  depends_on = [kubectl_manifest.backup_namespace]
}

resource "kubernetes_secret_v1" "backup_s3" {
  count = var.backup.enabled ? 1 : 0

  metadata {
    name      = "talos-backup-s3"
    namespace = "talos-backup"
  }
  data = {
    AWS_ACCESS_KEY_ID     = var.backup_access_key_id
    AWS_SECRET_ACCESS_KEY = var.backup_secret_access_key
  }
  depends_on = [kubectl_manifest.backup_namespace]
}

resource "kubectl_manifest" "backup_cronjob" {
  count = var.backup.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "batch/v1"
    kind       = "CronJob"
    metadata   = { name = "talos-backup", namespace = "talos-backup" }
    spec = {
      schedule                   = var.backup.schedule
      concurrencyPolicy          = "Forbid"
      successfulJobsHistoryLimit = 3
      failedJobsHistoryLimit     = 3
      jobTemplate = {
        spec = {
          backoffLimit = 2
          template = {
            spec = {
              restartPolicy = "OnFailure"
              tolerations   = [{ key = "node-role.kubernetes.io/control-plane", operator = "Exists", effect = "NoSchedule" }]
              containers = [{
                name       = "talos-backup"
                image      = "ghcr.io/siderolabs/talos-backup:${var.chart_versions.talos_backup}"
                workingDir = "/tmp"
                command    = ["/talos-backup"]
                env        = local.backup_env
                envFrom    = [{ secretRef = { name = "talos-backup-s3" } }]
                resources  = { requests = { cpu = "10m", memory = "64Mi" } }
                securityContext = {
                  runAsUser                = 1000
                  runAsGroup               = 1000
                  runAsNonRoot             = true
                  allowPrivilegeEscalation = false
                  capabilities             = { drop = ["ALL"] }
                  seccompProfile           = { type = "RuntimeDefault" }
                }
                volumeMounts = [
                  { mountPath = "/tmp", name = "tmp" },
                  { mountPath = "/.talos", name = "talos" },
                  { mountPath = "/var/run/secrets/talos.dev", name = "talos-secrets" },
                ]
              }]
              volumes = [
                { name = "tmp", emptyDir = {} },
                { name = "talos", emptyDir = {} },
                { name = "talos-secrets", secret = { secretName = "talos-backup-secrets" } },
              ]
            }
          }
        }
      }
    }
  })
  depends_on = [kubectl_manifest.backup_account, kubernetes_secret_v1.backup_s3]
}
