output "ingress_ip" {
  value = var.ingress_nginx.enabled ? try(data.kubernetes_service_v1.ingress_nginx[0].status[0].load_balancer[0].ingress[0].ip, "") : ""
}

output "argocd_ip" {
  value = var.argocd.enabled ? try(data.kubernetes_service_v1.argocd[0].status[0].load_balancer[0].ingress[0].ip, "") : ""
}

output "argocd_admin_password" {
  value     = var.argocd.enabled ? try(data.kubernetes_secret_v1.argocd_admin[0].data["password"], "") : ""
  sensitive = true
}
