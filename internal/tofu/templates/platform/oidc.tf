resource "kubectl_manifest" "oidc_admins" {
  count = var.oidc_admin_group != "" ? 1 : 0

  yaml_body = yamlencode({
    apiVersion = "rbac.authorization.k8s.io/v1"
    kind       = "ClusterRoleBinding"
    metadata   = { name = "kubit-oidc-admins" }
    roleRef    = { apiGroup = "rbac.authorization.k8s.io", kind = "ClusterRole", name = "cluster-admin" }
    subjects   = [{ apiGroup = "rbac.authorization.k8s.io", kind = "Group", name = var.oidc_admin_group }]
  })
}
