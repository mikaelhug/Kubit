variable "cluster_name" { type = string }
variable "cluster_endpoint" { type = string }
variable "talos_version" { type = string }
variable "kubernetes_version" { type = string }

# The control plane etcd was bootstrapped on.
variable "bootstrap_node" { type = string }

# true only when recreating the cluster from scratch on wiped nodes.
variable "bootstrap" {
  type    = bool
  default = false
}

variable "nodes" {
  type = map(object({
    ip   = string
    role = string # controlplane | worker
  }))
}
