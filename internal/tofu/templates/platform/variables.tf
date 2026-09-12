variable "kubeconfig" {
  description = "Path to the cluster admin kubeconfig"
  type        = string
}

variable "metallb" {
  type = object({
    enabled = bool
    range   = optional(string, "")
  })
}

variable "ingress_nginx" {
  type = object({ enabled = bool })
}

variable "gvisor" {
  type = object({ enabled = bool })
}

variable "metrics_server" {
  type = object({ enabled = bool })
}

variable "cert_manager" {
  type = object({ enabled = bool })
}

variable "argocd" {
  type = object({ enabled = bool })
}

# Chart versions are pinned by Kubit and bumped deliberately.
variable "chart_versions" {
  type = map(string)
  default = {
    metallb        = "0.16.1"
    ingress_nginx  = "4.15.1"
    metrics_server = "3.14.0"
    cert_manager   = "v1.21.2"
    argocd         = "10.9.0"
  }
}
