# ============================================================================
# Required Variables
# ============================================================================

variable "region" {
  description = "AWS region for all resources"
  type        = string
  default     = "us-east-1"
  # Common options: us-east-1, us-west-2, eu-west-1, eu-central-1, ap-southeast-1
}

variable "environment" {
  description = "Environment name — used in all resource names and tags"
  type        = string
  default     = "prod"
  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be dev, staging, or prod."
  }
}

variable "wachd_hostname" {
  description = "Public hostname for Wachd (used in TLS cert and OIDC redirect URI)"
  type        = string
  # Example: "wachd.mycompany.com" or "wachd.<ALB-DNS>.nip.io"
}

# ============================================================================
# VPC Configuration
# ============================================================================

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "single_nat_gateway" {
  description = "Use a single NAT gateway (saves cost; reduces HA). Set false for production."
  type        = bool
  default     = true
  # true  = 1 NAT gateway in AZ-1 only (dev/test: cheaper, single point of failure)
  # false = 1 NAT gateway per AZ (prod: HA, ~3x cost)
}

# ============================================================================
# EKS Configuration
# ============================================================================

variable "eks_kubernetes_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  default     = "1.31"
  # Check available versions: aws eks describe-addon-versions --query 'addons[0].addonVersions[].compatibilities[].clusterVersion' --output text | tr '\t' '\n' | sort -u
}

variable "eks_node_instance_type" {
  description = "EC2 instance type for EKS worker nodes"
  type        = string
  default     = "t3.medium"
  # Minimum for Wachd: t3.medium (2vCPU, 4GB) — fine for dev/test
  # Production recommended: m5.large (2vCPU, 8GB) or m5.xlarge (4vCPU, 16GB)
}

variable "eks_node_desired_count" {
  description = "Desired number of EKS worker nodes"
  type        = number
  default     = 3
}

variable "eks_node_min_count" {
  description = "Minimum number of EKS worker nodes (for cluster autoscaler)"
  type        = number
  default     = 2
}

variable "eks_node_max_count" {
  description = "Maximum number of EKS worker nodes (for cluster autoscaler)"
  type        = number
  default     = 10
}

variable "eks_public_access_cidrs" {
  description = "CIDRs allowed to reach the EKS public API endpoint (kubectl). Restrict to your office/VPN IPs for security."
  type        = list(string)
  default     = ["0.0.0.0/0"]
  # Example (restrict to office + CI): ["203.0.113.0/24", "198.51.100.10/32"]
}

# ============================================================================
# RDS PostgreSQL Configuration
# ============================================================================

variable "postgres_admin_login" {
  description = "Administrator username for RDS PostgreSQL"
  type        = string
  default     = "wachdadmin"
}

variable "postgres_instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.t3.medium"
  # Dev/test:    db.t3.medium  (2 vCPU, 4 GB) ~$50/month
  # Production:  db.m5.large   (2 vCPU, 8 GB) ~$140/month
  #              db.m5.xlarge  (4 vCPU, 16 GB) ~$280/month
}

variable "postgres_version" {
  description = "PostgreSQL major version"
  type        = string
  default     = "16"
}

variable "postgres_allocated_storage_gb" {
  description = "Allocated storage for RDS in GB (autoscales up to 4x this value)"
  type        = number
  default     = 32
}

variable "postgres_multi_az" {
  description = "Enable Multi-AZ deployment for RDS (recommended for production)"
  type        = bool
  default     = false
  # true = standby replica in another AZ, automatic failover (~60s)
}

variable "postgres_backup_retention_days" {
  description = "Number of days to retain automated RDS backups (0 disables backups)"
  type        = number
  default     = 7
}

# ============================================================================
# ElastiCache Redis Configuration
# ============================================================================

variable "redis_node_type" {
  description = "ElastiCache Redis node type"
  type        = string
  default     = "cache.t3.medium"
  # Dev/test:    cache.t3.micro   (0.5 GB) — not suitable for production
  # Standard:    cache.t3.medium  (3.09 GB) — sufficient for Wachd job queue
  # Production:  cache.m5.large   (6.38 GB)
}

variable "redis_num_replicas" {
  description = "Number of Redis read replicas (0 = primary only, 1+ = replication with auto-failover)"
  type        = number
  default     = 0
  # 0 = single node (dev/test, no failover)
  # 1 = primary + 1 replica (production recommended)
}

# ============================================================================
# ECR (Optional)
# ============================================================================

variable "create_ecr" {
  description = "Create an ECR repository to host the Wachd image in your own account"
  type        = bool
  default     = false
  # Set false if using ghcr.io/wachd/wachd or another external registry
}

# ============================================================================
# Amazon Managed Grafana (Optional)
# ============================================================================

variable "create_grafana" {
  description = "Create an Amazon Managed Grafana workspace"
  type        = bool
  default     = true
  # SAML authentication must be configured separately after creation
}

# ============================================================================
# Cognito (Optional — SSO Identity Provider)
# ============================================================================

variable "create_cognito" {
  description = "Create a Cognito User Pool as an OIDC identity provider for Wachd SSO"
  type        = bool
  default     = false
  # Set false if you already have Okta, Auth0, Ping, or another OIDC provider
}

# ============================================================================
# Tagging
# ============================================================================

variable "tags" {
  description = "Tags applied to all AWS resources"
  type        = map(string)
  default = {
    Project     = "wachd"
    ManagedBy   = "terraform"
    Environment = "prod"
  }
}
