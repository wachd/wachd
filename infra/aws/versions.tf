terraform {
  required_version = ">= 1.6"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.30"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.13"
    }
    # gavinbunney/kubectl is used instead of kubernetes_manifest for ESO CRDs
    # because it does NOT validate the CRD schema at plan time — essential when
    # the CRDs don't exist yet on first apply (i.e. before ESO is installed).
    kubectl = {
      source  = "gavinbunney/kubectl"
      version = "~> 1.14"
    }
    null = {
      source  = "hashicorp/null"
      version = "~> 3.2"
    }
  }

  # Uncomment to store state in S3 (recommended for teams)
  # backend "s3" {
  #   bucket         = "wachd-tfstate"
  #   key            = "wachd/prod/terraform.tfstate"
  #   region         = "us-east-1"
  #   encrypt        = true
  #   dynamodb_table = "wachd-tfstate-lock"
  # }
}

provider "aws" {
  region = var.region

  default_tags {
    tags = var.tags
  }
}

# Kubernetes, Helm, and kubectl providers are configured from EKS cluster outputs.
# Terraform evaluates these lazily — they initialize only when a resource that needs
# them is processed, by which time the EKS cluster and data sources are resolved.

provider "kubernetes" {
  host                   = data.aws_eks_cluster.wachd.endpoint
  cluster_ca_certificate = base64decode(data.aws_eks_cluster.wachd.certificate_authority[0].data)
  token                  = data.aws_eks_cluster_auth.wachd.token
}

provider "helm" {
  kubernetes {
    host                   = data.aws_eks_cluster.wachd.endpoint
    cluster_ca_certificate = base64decode(data.aws_eks_cluster.wachd.certificate_authority[0].data)
    token                  = data.aws_eks_cluster_auth.wachd.token
  }
}

provider "kubectl" {
  host                   = data.aws_eks_cluster.wachd.endpoint
  cluster_ca_certificate = base64decode(data.aws_eks_cluster.wachd.certificate_authority[0].data)
  token                  = data.aws_eks_cluster_auth.wachd.token
  load_config_file       = false
}

provider "tls" {}
provider "random" {}
