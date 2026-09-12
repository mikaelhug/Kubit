# Exported by Kubit: the Talos layer of this cluster as OpenTofu, for operators who want
# to leave Kubit behind. Kubit never runs this root itself.
terraform {
  required_version = ">= 1.9"
  required_providers {
    talos = {
      source  = "siderolabs/talos"
      version = ">= 0.11"
    }
  }
}
