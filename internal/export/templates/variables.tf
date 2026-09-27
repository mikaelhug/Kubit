variable "cluster_name" { type = string }
variable "cluster_endpoint" { type = string }
variable "talos_version" { type = string }
variable "kubernetes_version" { type = string }

variable "bootstrap_node" { type = string }

variable "bootstrap" {
  type    = bool
  default = false
}

variable "nodes" {
  type = map(object({
    ip   = string
    role = string
  }))
}
