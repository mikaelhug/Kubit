import {
  to = talos_machine_secrets.this
  id = "secrets.yaml"
}

resource "talos_machine_secrets" "this" {
  talos_version = var.talos_version

  lifecycle {
    ignore_changes = [talos_version]
  }
}

resource "talos_machine_configuration_apply" "node" {
  for_each = var.nodes

  client_configuration        = talos_machine_secrets.this.client_configuration
  machine_configuration_input = file("${path.module}/machineconfigs/${each.key}.yaml")
  node                        = each.value.ip
  endpoint                    = each.value.ip
}

resource "talos_machine_bootstrap" "this" {
  count      = var.bootstrap ? 1 : 0
  depends_on = [talos_machine_configuration_apply.node]

  node                 = var.bootstrap_node
  client_configuration = talos_machine_secrets.this.client_configuration
}

resource "talos_cluster_kubeconfig" "this" {
  depends_on = [talos_machine_configuration_apply.node, talos_machine_bootstrap.this]

  node                 = var.bootstrap_node
  client_configuration = talos_machine_secrets.this.client_configuration
}

data "talos_client_configuration" "this" {
  cluster_name         = var.cluster_name
  client_configuration = talos_machine_secrets.this.client_configuration
  endpoints            = [for n in var.nodes : n.ip if n.role == "controlplane"]
}

output "talosconfig" {
  value     = data.talos_client_configuration.this.talos_config
  sensitive = true
}

output "kubeconfig" {
  value     = talos_cluster_kubeconfig.this.kubeconfig_raw
  sensitive = true
}
