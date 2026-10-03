output "ingress_ip" {
  value = var.traefik.enabled ? try(data.kubernetes_service_v1.traefik[0].status[0].load_balancer[0].ingress[0].ip, "") : ""
}
