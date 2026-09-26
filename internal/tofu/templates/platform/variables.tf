variable "kubeconfig" {
  description = "Path to the cluster admin kubeconfig"
  type        = string
}

# Each add-on carries free-form Helm values from cluster.yaml (platform.<addon>.values),
# merged over Kubit's defaults.
variable "metallb" {
  type = object({
    enabled = bool
    range   = optional(string, "")
    pool    = optional(string, "")
    values  = optional(any, {})
  })
}

variable "ingress_nginx" {
  type = object({ enabled = bool, values = optional(any, {}) })
}

variable "gvisor" {
  type = object({ enabled = bool, values = optional(any, {}) })
}

variable "metrics_server" {
  type = object({ enabled = bool, values = optional(any, {}) })
}

variable "cert_manager" {
  type = object({ enabled = bool, values = optional(any, {}) })
}

variable "flux" {
  type = object({
    enabled    = bool
    values     = optional(any, {})
    repository = optional(object({ url = string, branch = string, path = string, interval = string }))
  })
}

variable "builds" {
  type = object({
    enabled       = bool
    ip            = optional(string, "")
    registry_size = optional(string, "5Gi")
  })
  default = { enabled = false }
}

variable "longhorn" {
  type = object({ enabled = bool, values = optional(any, {}), replicas = optional(number, 3) })
}

# The SSO group (with its oidc: prefix) bound to cluster-admin; empty binds nothing.
variable "oidc_admin_group" {
  type    = string
  default = ""
}

# Chart versions are pinned by Kubit and bumped deliberately.
variable "chart_versions" {
  type = map(string)
  default = {
    metallb        = "0.16.1"
    ingress_nginx  = "4.15.1"
    metrics_server = "3.14.0"
    cert_manager   = "v1.21.2"
    flux           = "2.19.1"
    longhorn       = "1.10.1"
  }
}
