terraform {
  required_version = ">= 1.9"
  backend "kubernetes" {}
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "~> 3.3"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 3.2"
    }
    kubectl = {
      source  = "alekc/kubectl"
      version = "~> 3.0"
    }
  }
}

provider "helm" {
  kubernetes = {
    config_path = var.kubeconfig
  }
}

provider "kubernetes" {
  config_path = var.kubeconfig
}

provider "kubectl" {
  config_path      = var.kubeconfig
  load_config_file = true
}
