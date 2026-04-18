terraform {
  required_version = ">= 1.6"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
    azuread = {
      source  = "hashicorp/azuread"
      version = "~> 2.50"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.30"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.13"
    }
    kubectl = {
      source  = "gavinbunney/kubectl"
      version = "~> 1.14"
    }
    null = {
      source  = "hashicorp/null"
      version = "~> 3.2"
    }
  }

  # Uncomment to store state in Azure Blob Storage (recommended for teams)
  # backend "azurerm" {
  #   resource_group_name  = "rg-wachd-tfstate"
  #   storage_account_name = "wachdtfstate"
  #   container_name       = "tfstate"
  #   key                  = "wachd.tfstate"
  # }
}

provider "azurerm" {
  features {
    resource_group {
      prevent_deletion_if_contains_resources = false
    }
    key_vault {
      purge_soft_delete_on_destroy    = true
      recover_soft_deleted_key_vaults = true
    }
  }
}

provider "azuread" {}

provider "random" {}

# Kubernetes, Helm, and kubectl providers are configured from AKS cluster outputs.
# They initialize lazily — only when a resource that needs them is processed,
# by which time the AKS cluster already exists.

provider "kubernetes" {
  host                   = azurerm_kubernetes_cluster.wachd.kube_admin_config[0].host
  client_certificate     = base64decode(azurerm_kubernetes_cluster.wachd.kube_admin_config[0].client_certificate)
  client_key             = base64decode(azurerm_kubernetes_cluster.wachd.kube_admin_config[0].client_key)
  cluster_ca_certificate = base64decode(azurerm_kubernetes_cluster.wachd.kube_admin_config[0].cluster_ca_data)
}

provider "helm" {
  kubernetes {
    host                   = azurerm_kubernetes_cluster.wachd.kube_admin_config[0].host
    client_certificate     = base64decode(azurerm_kubernetes_cluster.wachd.kube_admin_config[0].client_certificate)
    client_key             = base64decode(azurerm_kubernetes_cluster.wachd.kube_admin_config[0].client_key)
    cluster_ca_certificate = base64decode(azurerm_kubernetes_cluster.wachd.kube_admin_config[0].cluster_ca_data)
  }
}

provider "kubectl" {
  host                   = azurerm_kubernetes_cluster.wachd.kube_admin_config[0].host
  client_certificate     = base64decode(azurerm_kubernetes_cluster.wachd.kube_admin_config[0].client_certificate)
  client_key             = base64decode(azurerm_kubernetes_cluster.wachd.kube_admin_config[0].client_key)
  cluster_ca_certificate = base64decode(azurerm_kubernetes_cluster.wachd.kube_admin_config[0].cluster_ca_data)
  load_config_file       = false
}
