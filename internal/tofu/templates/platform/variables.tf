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

variable "traefik" {
  type = object({ enabled = bool, values = optional(any, {}) })
}

variable "ingress_ip_pin" {
  type    = string
  default = ""
}

variable "gvisor" {
  type = object({ enabled = bool })
}

variable "metrics_server" {
  type = object({ enabled = bool, values = optional(any, {}) })
}

variable "cert_manager" {
  type = object({ enabled = bool, values = optional(any, {}) })
}

variable "flux" {
  type = object({
    enabled          = bool
    image_automation = optional(bool, false)
    values           = optional(any, {})
    repository       = optional(object({ url = string, branch = string, path = string, interval = string }))
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

variable "backup" {
  type = object({
    enabled        = bool
    schedule       = optional(string, "")
    bucket         = optional(string, "")
    region         = optional(string, "")
    endpoint       = optional(string, "")
    prefix         = optional(string, "")
    path_style     = optional(bool, false)
    compression    = optional(bool, false)
    age_recipients = optional(list(string), [])
    cluster        = optional(string, "")
  })
  default = { enabled = false }
}

variable "flux_git_identity" {
  type      = string
  default   = ""
  sensitive = true
}

variable "flux_git_known_hosts" {
  type    = string
  default = ""
}

variable "backup_access_key_id" {
  type      = string
  default   = ""
  sensitive = true
}

variable "backup_secret_access_key" {
  type      = string
  default   = ""
  sensitive = true
}
