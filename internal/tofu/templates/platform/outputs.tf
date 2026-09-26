output "ingress_ip" {
  value = var.ingress_nginx.enabled ? try(data.kubernetes_service_v1.ingress_nginx[0].status[0].load_balancer[0].ingress[0].ip, "") : ""
}
