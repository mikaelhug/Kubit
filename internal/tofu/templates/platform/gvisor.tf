# Talos ships runsc via the siderolabs/gvisor extension; Kubit labels nodes that carry it
# and, separately, those with /dev/kvm for the faster KVM platform.
resource "kubectl_manifest" "runtimeclass_gvisor" {
  count = var.gvisor.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "node.k8s.io/v1"
    kind       = "RuntimeClass"
    metadata   = { name = "gvisor" }
    handler    = "runsc"
    scheduling = { nodeSelector = { "sandbox.runtime/gvisor" = "true" } }
  })
}

resource "kubectl_manifest" "runtimeclass_gvisor_kvm" {
  count = var.gvisor.enabled ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "node.k8s.io/v1"
    kind       = "RuntimeClass"
    metadata   = { name = "gvisor-kvm" }
    handler    = "runsc-kvm"
    scheduling = { nodeSelector = { "sandbox.runtime/gvisor-kvm" = "true" } }
  })
}
