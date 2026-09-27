variable "kubeconfig" {
  description = "Path to the cluster admin kubeconfig"
  type        = string
}

variable "metallb" {
  type = object({
    enabled = bool
    range   = optional(string, "")
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

variable "oidc_admin_group" {
  type    = string
  default = ""
}

variable "chart_versions" {
  type = map(string)
}
