# The cluster PKI is imported from secrets.yaml, the same bundle Kubit generated, so the
# provider signs with the certificates the nodes already trust.
import {
  to = talos_machine_secrets.this
  id = "secrets.yaml"
}

resource "talos_machine_secrets" "this" {
  talos_version = var.talos_version

  # The provider records an old version contract on import; letting the declared
  # version "change" it would regenerate the whole PKI.
  lifecycle {
    ignore_changes = [talos_version]
  }
}

# Machine configs are the exact multi-document files Kubit applied (machineconfigs/),
# not regenerated from patches, so the export matches the nodes byte for byte.
resource "talos_machine_configuration_apply" "node" {
  for_each = var.nodes

  client_configuration        = talos_machine_secrets.this.client_configuration
  machine_configuration_input = file("${path.module}/machineconfigs/${each.key}.yaml")
  node                        = each.value.ip
  endpoint                    = each.value.ip
}

# etcd was bootstrapped by Kubit; the provider cannot import that fact and fails with
# AlreadyExists if asked to bootstrap again, so this resource only exists when
# recreating the cluster on wiped nodes (var.bootstrap = true).
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
