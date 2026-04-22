# ============================================================================
# Data Sources
# ============================================================================

data "aws_caller_identity" "current" {}

data "aws_availability_zones" "available" {
  state = "available"
}

# EKS cluster data sources — used to configure kubernetes/helm/kubectl providers
data "aws_eks_cluster" "wachd" {
  name       = aws_eks_cluster.wachd.name
  depends_on = [aws_eks_cluster.wachd]
}

data "aws_eks_cluster_auth" "wachd" {
  name       = aws_eks_cluster.wachd.name
  depends_on = [aws_eks_cluster.wachd]
}

# ============================================================================
# Random Passwords and Keys
# ============================================================================

resource "random_password" "postgres" {
  length           = 24
  special          = true
  override_special = "!#$%^&*()-_=+[]{}|;:,.<>?"
  min_upper        = 2
  min_lower        = 2
  min_numeric      = 2
  min_special      = 2
}

# ElastiCache Redis auth token: no @, /, ", space, or control characters
resource "random_password" "redis" {
  length      = 32
  special     = false
  min_upper   = 2
  min_lower   = 2
  min_numeric = 2
}

resource "random_id" "wachd_encryption_key" {
  byte_length = 32
  # .hex produces 64 lowercase hex characters — matches what Go expects for AES-256-GCM
}

resource "random_string" "ecr_suffix" {
  length  = 6
  upper   = false
  special = false
}

# ============================================================================
# VPC Pre-destroy Cleanup
# Deletes any AWS Load Balancers created by Kubernetes (nginx ingress, etc.)
# that Terraform doesn't manage. These block VPC/subnet deletion if left behind.
#
# How it works: by depending on the subnets, this resource is destroyed FIRST
# (Terraform reverses dependency order on destroy), so the cleanup provisioner
# runs before Terraform tries to delete subnets and the IGW.
# ============================================================================

resource "null_resource" "cleanup_k8s_loadbalancers" {
  # Triggers ensure the stored vpc_id/region are always current
  triggers = {
    region = var.region
    vpc_id = aws_vpc.wachd.id
  }

  # Depends on subnets so this is destroyed before subnets/IGW on terraform destroy
  depends_on = [
    aws_subnet.public,
    aws_subnet.private,
    aws_internet_gateway.wachd,
  ]

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      echo "Cleaning up Kubernetes-created load balancers in VPC ${self.triggers.vpc_id}..."

      # Delete Classic ELBs (nginx ingress on older clusters creates these)
      for name in $(aws elb describe-load-balancers --region ${self.triggers.region} \
          --query "LoadBalancerDescriptions[?VPCId=='${self.triggers.vpc_id}'].LoadBalancerName" \
          --output text); do
        echo "Deleting Classic ELB: $name"
        aws elb delete-load-balancer --region ${self.triggers.region} --load-balancer-name "$name"
      done

      # Delete ALBs/NLBs (AWS Load Balancer Controller creates these)
      for arn in $(aws elbv2 describe-load-balancers --region ${self.triggers.region} \
          --query "LoadBalancers[?VpcId=='${self.triggers.vpc_id}'].LoadBalancerArn" \
          --output text); do
        echo "Deleting ALB/NLB: $arn"
        aws elbv2 delete-load-balancer --region ${self.triggers.region} --load-balancer-arn "$arn"
      done

      # Wait for ELB deletions to propagate (ENIs take ~30s to release after ELB is deleted)
      echo "Waiting 45s for ELB ENIs to release..."
      sleep 45

      echo "Load balancer cleanup complete."
    EOT
  }
}

# ============================================================================
# VPC
# ============================================================================

resource "aws_vpc" "wachd" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "vpc-wachd-${var.environment}"
  }
}

# Public subnets — for load balancers (nginx ingress)
resource "aws_subnet" "public" {
  count = 3

  vpc_id                  = aws_vpc.wachd.id
  cidr_block              = cidrsubnet(var.vpc_cidr, 8, count.index)
  availability_zone       = data.aws_availability_zones.available.names[count.index]
  map_public_ip_on_launch = true

  tags = {
    Name                                             = "subnet-public-${count.index + 1}-wachd-${var.environment}"
    "kubernetes.io/role/elb"                         = "1"
    "kubernetes.io/cluster/eks-wachd-${var.environment}" = "shared"
  }
}

# Private subnets — for EKS nodes, RDS, and ElastiCache
resource "aws_subnet" "private" {
  count = 3

  vpc_id            = aws_vpc.wachd.id
  cidr_block        = cidrsubnet(var.vpc_cidr, 8, count.index + 10)
  availability_zone = data.aws_availability_zones.available.names[count.index]

  tags = {
    Name                                             = "subnet-private-${count.index + 1}-wachd-${var.environment}"
    "kubernetes.io/role/internal-elb"                = "1"
    "kubernetes.io/cluster/eks-wachd-${var.environment}" = "shared"
  }
}

resource "aws_internet_gateway" "wachd" {
  vpc_id = aws_vpc.wachd.id

  tags = {
    Name = "igw-wachd-${var.environment}"
  }
}

resource "aws_eip" "nat" {
  # Single NAT saves cost (dev/test); one per AZ gives HA (prod)
  count  = var.single_nat_gateway ? 1 : 3
  domain = "vpc"

  tags = {
    Name = "eip-nat-${count.index + 1}-wachd-${var.environment}"
  }

  depends_on = [aws_internet_gateway.wachd]
}

resource "aws_nat_gateway" "wachd" {
  count         = var.single_nat_gateway ? 1 : 3
  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id

  tags = {
    Name = "nat-${count.index + 1}-wachd-${var.environment}"
  }

  depends_on = [aws_internet_gateway.wachd]
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.wachd.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.wachd.id
  }

  tags = {
    Name = "rt-public-wachd-${var.environment}"
  }
}

resource "aws_route_table_association" "public" {
  count          = 3
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table" "private" {
  count  = var.single_nat_gateway ? 1 : 3
  vpc_id = aws_vpc.wachd.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = var.single_nat_gateway ? aws_nat_gateway.wachd[0].id : aws_nat_gateway.wachd[count.index].id
  }

  tags = {
    Name = "rt-private-${count.index + 1}-wachd-${var.environment}"
  }
}

resource "aws_route_table_association" "private" {
  count          = 3
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = var.single_nat_gateway ? aws_route_table.private[0].id : aws_route_table.private[count.index].id
}

# ============================================================================
# Security Groups
# ============================================================================

# Additional security group for EKS nodes
resource "aws_security_group" "eks_nodes" {
  name        = "eks-nodes-wachd-${var.environment}"
  description = "EKS worker nodes"
  vpc_id      = aws_vpc.wachd.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow all outbound"
  }

  tags = {
    Name = "sg-eks-nodes-wachd-${var.environment}"
  }
}

resource "aws_security_group" "rds" {
  name        = "rds-wachd-${var.environment}"
  description = "RDS PostgreSQL - allow from VPC only"
  vpc_id      = aws_vpc.wachd.id

  ingress {
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
    description = "PostgreSQL from VPC"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow all outbound"
  }

  tags = {
    Name = "sg-rds-wachd-${var.environment}"
  }
}

resource "aws_security_group" "redis" {
  name        = "redis-wachd-${var.environment}"
  description = "ElastiCache Redis - allow from VPC only"
  vpc_id      = aws_vpc.wachd.id

  ingress {
    from_port   = 6379
    to_port     = 6379
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
    description = "Redis from VPC"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow all outbound"
  }

  tags = {
    Name = "sg-redis-wachd-${var.environment}"
  }
}

# ============================================================================
# EKS Cluster IAM Role
# ============================================================================

resource "aws_iam_role" "eks_cluster" {
  name = "role-eks-cluster-wachd-${var.environment}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "eks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "eks_cluster_policy" {
  role       = aws_iam_role.eks_cluster.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
}

# ============================================================================
# EKS Cluster
# ============================================================================

resource "aws_cloudwatch_log_group" "eks" {
  name              = "/aws/eks/eks-wachd-${var.environment}/cluster"
  retention_in_days = 30
}

resource "aws_eks_cluster" "wachd" {
  name     = "eks-wachd-${var.environment}"
  role_arn = aws_iam_role.eks_cluster.arn
  version  = var.eks_kubernetes_version

  vpc_config {
    subnet_ids              = concat(aws_subnet.private[*].id, aws_subnet.public[*].id)
    security_group_ids      = [aws_security_group.eks_nodes.id]
    endpoint_private_access = true
    endpoint_public_access  = true
    public_access_cidrs     = var.eks_public_access_cidrs
  }

  enabled_cluster_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]

  access_config {
    authentication_mode                         = "API_AND_CONFIG_MAP"
    bootstrap_cluster_creator_admin_permissions = true
  }

  depends_on = [
    aws_iam_role_policy_attachment.eks_cluster_policy,
    aws_cloudwatch_log_group.eks,
  ]
}

# OIDC provider — enables IRSA (IAM Roles for Service Accounts) on the cluster
data "tls_certificate" "eks" {
  url = aws_eks_cluster.wachd.identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "eks" {
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.eks.certificates[0].sha1_fingerprint]
  url             = aws_eks_cluster.wachd.identity[0].oidc[0].issuer
}

# ============================================================================
# EKS Node Group IAM Role
# ============================================================================

resource "aws_iam_role" "eks_nodes" {
  name = "role-eks-nodes-wachd-${var.environment}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "eks_worker_node" {
  role       = aws_iam_role.eks_nodes.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy"
}

resource "aws_iam_role_policy_attachment" "eks_cni" {
  role       = aws_iam_role.eks_nodes.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy"
}

resource "aws_iam_role_policy_attachment" "eks_ecr_readonly" {
  role       = aws_iam_role.eks_nodes.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

resource "aws_iam_role_policy_attachment" "eks_cloudwatch" {
  role       = aws_iam_role.eks_nodes.name
  policy_arn = "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy"
}

# ============================================================================
# EKS Managed Node Group
# ============================================================================

resource "aws_eks_node_group" "wachd" {
  cluster_name    = aws_eks_cluster.wachd.name
  node_group_name = "ng-wachd-${var.environment}"
  node_role_arn   = aws_iam_role.eks_nodes.arn
  subnet_ids      = aws_subnet.private[*].id

  instance_types = [var.eks_node_instance_type]
  ami_type       = "AL2_x86_64"

  scaling_config {
    desired_size = var.eks_node_desired_count
    min_size     = var.eks_node_min_count
    max_size     = var.eks_node_max_count
  }

  update_config {
    max_unavailable = 1
  }

  depends_on = [
    aws_iam_role_policy_attachment.eks_worker_node,
    aws_iam_role_policy_attachment.eks_cni,
    aws_iam_role_policy_attachment.eks_ecr_readonly,
  ]

  lifecycle {
    ignore_changes = [scaling_config[0].desired_size]
  }
}

# ============================================================================
# EKS Cluster Add-ons
# ============================================================================

resource "aws_eks_addon" "vpc_cni" {
  cluster_name = aws_eks_cluster.wachd.name
  addon_name   = "vpc-cni"
  depends_on   = [aws_eks_node_group.wachd]
}

resource "aws_eks_addon" "coredns" {
  cluster_name = aws_eks_cluster.wachd.name
  addon_name   = "coredns"
  depends_on   = [aws_eks_node_group.wachd]
}

resource "aws_eks_addon" "kube_proxy" {
  cluster_name = aws_eks_cluster.wachd.name
  addon_name   = "kube-proxy"
  depends_on   = [aws_eks_node_group.wachd]
}

resource "aws_eks_addon" "ebs_csi_driver" {
  cluster_name             = aws_eks_cluster.wachd.name
  addon_name               = "aws-ebs-csi-driver"
  service_account_role_arn = aws_iam_role.ebs_csi.arn
  depends_on               = [aws_eks_node_group.wachd]
}


# IRSA role for EBS CSI driver
resource "aws_iam_role" "ebs_csi" {
  name = "role-ebs-csi-wachd-${var.environment}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = aws_iam_openid_connect_provider.eks.arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "${replace(aws_iam_openid_connect_provider.eks.url, "https://", "")}:sub" = "system:serviceaccount:kube-system:ebs-csi-controller-sa"
          "${replace(aws_iam_openid_connect_provider.eks.url, "https://", "")}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "ebs_csi_policy" {
  role       = aws_iam_role.ebs_csi.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

# ============================================================================
# RDS PostgreSQL
# ============================================================================

resource "aws_db_subnet_group" "wachd" {
  name       = "dbsg-wachd-${var.environment}"
  subnet_ids = aws_subnet.private[*].id

  tags = {
    Name = "dbsg-wachd-${var.environment}"
  }
}

resource "aws_db_instance" "wachd" {
  identifier = "rds-wachd-${var.environment}"

  engine         = "postgres"
  engine_version = var.postgres_version
  instance_class = var.postgres_instance_class

  db_name  = "wachd"
  username = var.postgres_admin_login
  password = random_password.postgres.result

  allocated_storage     = var.postgres_allocated_storage_gb
  max_allocated_storage = var.postgres_allocated_storage_gb * 4
  storage_type          = "gp3"
  storage_encrypted     = true

  multi_az               = var.postgres_multi_az
  db_subnet_group_name   = aws_db_subnet_group.wachd.name
  vpc_security_group_ids = [aws_security_group.rds.id]

  backup_retention_period = var.postgres_backup_retention_days
  backup_window           = "03:00-04:00"
  maintenance_window      = "Mon:04:00-Mon:05:00"

  deletion_protection = false    # Set true for production
  skip_final_snapshot = true     # Set false for production and set final_snapshot_identifier

  performance_insights_enabled = true
  monitoring_interval          = 0  # Enhanced monitoring disabled (requires a separate IAM role)

  tags = {
    Name = "rds-wachd-${var.environment}"
  }

  lifecycle {
    ignore_changes = [password]
  }
}

# ============================================================================
# ElastiCache Redis
# ============================================================================

resource "aws_elasticache_subnet_group" "wachd" {
  name       = "ecsg-wachd-${var.environment}"
  subnet_ids = aws_subnet.private[*].id

  tags = {
    Name = "ecsg-wachd-${var.environment}"
  }
}

resource "aws_elasticache_replication_group" "wachd" {
  replication_group_id = "redis-wachd-${var.environment}"
  description          = "Wachd job queue and session cache"

  engine               = "redis"
  engine_version       = "7.1"
  node_type            = var.redis_node_type
  num_cache_clusters   = var.redis_num_replicas + 1  # primary + replicas
  port                 = 6379
  parameter_group_name = "default.redis7"

  # Security: TLS in-transit + auth token
  transit_encryption_enabled = true
  at_rest_encryption_enabled = true
  auth_token                 = random_password.redis.result

  subnet_group_name  = aws_elasticache_subnet_group.wachd.name
  security_group_ids = [aws_security_group.redis.id]

  # Automatic failover (requires num_cache_clusters >= 2)
  automatic_failover_enabled = var.redis_num_replicas > 0 ? true : false
  multi_az_enabled           = var.redis_num_replicas > 0 ? true : false

  snapshot_retention_limit = 1
  snapshot_window          = "05:00-06:00"
  maintenance_window       = "Mon:06:00-Mon:07:00"

  tags = {
    Name = "redis-wachd-${var.environment}"
  }

  lifecycle {
    ignore_changes = [auth_token]
  }
}

# ============================================================================
# ECR (Optional — set create_ecr = true to host Wachd image in your own registry)
# ============================================================================

resource "aws_ecr_repository" "wachd" {
  count = var.create_ecr ? 1 : 0

  name                 = "wachd-${random_string.ecr_suffix.result}"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }
}

# Grant EKS nodes pull access (avoids any secret for ECR auth within the same account)
resource "aws_ecr_repository_policy" "wachd" {
  count      = var.create_ecr ? 1 : 0
  repository = aws_ecr_repository.wachd[0].name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = aws_iam_role.eks_nodes.arn }
      Action    = ["ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage", "ecr:BatchCheckLayerAvailability"]
    }]
  })
}

# ============================================================================
# Amazon Managed Prometheus (AMP)
# ============================================================================

resource "aws_prometheus_workspace" "wachd" {
  alias = "amp-wachd-${var.environment}"

  logging_configuration {
    log_group_arn = "${aws_cloudwatch_log_group.amp.arn}:*"
  }

  tags = {
    Name = "amp-wachd-${var.environment}"
  }
}

resource "aws_cloudwatch_log_group" "amp" {
  name              = "/aws/prometheus/wachd-${var.environment}"
  retention_in_days = 30
}

# IRSA role for ADOT collector (EKS → AMP remote-write)
resource "aws_iam_role" "adot" {
  name = "role-adot-wachd-${var.environment}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = aws_iam_openid_connect_provider.eks.arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "${replace(aws_iam_openid_connect_provider.eks.url, "https://", "")}:sub" = "system:serviceaccount:monitoring:adot-collector"
          "${replace(aws_iam_openid_connect_provider.eks.url, "https://", "")}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "adot_amp" {
  role       = aws_iam_role.adot.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonPrometheusRemoteWriteAccess"
}

# ============================================================================
# Amazon Managed Grafana (AMG) — optional
# ============================================================================

resource "aws_grafana_workspace" "wachd" {
  count = var.create_grafana ? 1 : 0

  name                     = "grafana-wachd-${var.environment}"
  account_access_type      = "CURRENT_ACCOUNT"
  authentication_providers = ["SAML"]
  permission_type          = "SERVICE_MANAGED"

  data_sources = ["PROMETHEUS"]

  role_arn = aws_iam_role.grafana[0].arn

  tags = {
    Name = "grafana-wachd-${var.environment}"
  }
}

resource "aws_iam_role" "grafana" {
  count = var.create_grafana ? 1 : 0
  name  = "role-grafana-wachd-${var.environment}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "grafana.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "grafana_amp" {
  count      = var.create_grafana ? 1 : 0
  role       = aws_iam_role.grafana[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonPrometheusQueryAccess"
}

# ============================================================================
# Cognito User Pool (optional — equivalent to Entra App Registration for SSO)
# ============================================================================

resource "aws_cognito_user_pool" "wachd" {
  count = var.create_cognito ? 1 : 0

  name = "userpool-wachd-${var.environment}"

  # Wachd maps IdP groups to team roles — require groups in token
  schema {
    name                = "groups"
    attribute_data_type = "String"
    mutable             = true
    string_attribute_constraints {}
  }

  password_policy {
    minimum_length    = 12
    require_lowercase = true
    require_uppercase = true
    require_numbers   = true
    require_symbols   = true
  }

  auto_verified_attributes = ["email"]

  username_attributes = ["email"]

  tags = {
    Name = "userpool-wachd-${var.environment}"
  }
}

resource "aws_cognito_user_pool_client" "wachd" {
  count = var.create_cognito ? 1 : 0

  name         = "wachd-app-client"
  user_pool_id = aws_cognito_user_pool.wachd[0].id

  generate_secret = true

  allowed_oauth_flows                  = ["code"]
  allowed_oauth_flows_user_pool_client = true
  allowed_oauth_scopes                 = ["openid", "email", "profile"]

  callback_urls = ["https://${var.wachd_hostname}/auth/callback"]
  logout_urls   = ["https://${var.wachd_hostname}/"]

  supported_identity_providers = ["COGNITO"]

  explicit_auth_flows = [
    "ALLOW_USER_SRP_AUTH",
    "ALLOW_REFRESH_TOKEN_AUTH",
  ]
}

resource "aws_cognito_user_pool_domain" "wachd" {
  count = var.create_cognito ? 1 : 0

  domain       = "wachd-${var.environment}-${data.aws_caller_identity.current.account_id}"
  user_pool_id = aws_cognito_user_pool.wachd[0].id
}

# ============================================================================
# AWS Secrets Manager
# All secrets are created here. ESO (External Secrets Operator) syncs them
# into Kubernetes automatically. DevOps engineers never run kubectl create secret.
# ============================================================================

locals {
  secret_prefix = "wachd/${var.environment}"
}

# Core secrets — auto-generated by Terraform
resource "aws_secretsmanager_secret" "postgres_password" {
  name                    = "${local.secret_prefix}/postgres-password"
  description             = "Wachd PostgreSQL admin password"
  recovery_window_in_days = 0  # Allow immediate deletion (set 7+ for prod)
}

resource "aws_secretsmanager_secret_version" "postgres_password" {
  secret_id     = aws_secretsmanager_secret.postgres_password.id
  secret_string = random_password.postgres.result
}

resource "aws_secretsmanager_secret" "redis_auth_token" {
  name                    = "${local.secret_prefix}/redis-auth-token"
  description             = "Wachd ElastiCache Redis auth token"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "redis_auth_token" {
  secret_id     = aws_secretsmanager_secret.redis_auth_token.id
  secret_string = random_password.redis.result
}

resource "aws_secretsmanager_secret" "encryption_key" {
  name                    = "${local.secret_prefix}/encryption-key"
  description             = "Wachd AES-256-GCM encryption key for secrets at rest"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "encryption_key" {
  secret_id     = aws_secretsmanager_secret.encryption_key.id
  secret_string = random_id.wachd_encryption_key.hex
}

# Optional secrets — created as empty placeholders.
# DevOps engineers populate these in the AWS Console or via CLI.
# ESO automatically syncs updated values to K8s within refreshInterval (1h by default).
resource "aws_secretsmanager_secret" "oidc_client_secret" {
  name                    = "${local.secret_prefix}/oidc-client-secret"
  description             = "OIDC/SSO client secret (Cognito, Okta, Entra - any OIDC provider). Add your secret here."
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "oidc_client_secret" {
  secret_id     = aws_secretsmanager_secret.oidc_client_secret.id
  secret_string = var.create_cognito ? try(aws_cognito_user_pool_client.wachd[0].client_secret, "placeholder") : "placeholder"
}

resource "aws_secretsmanager_secret" "slack_webhook_url" {
  name                    = "${local.secret_prefix}/slack-webhook-url"
  description             = "Slack incoming webhook URL for Wachd notifications. Update to enable Slack alerts."
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "slack_webhook_url" {
  secret_id     = aws_secretsmanager_secret.slack_webhook_url.id
  secret_string = "placeholder"  # Update in Secrets Manager to enable Slack notifications
}

resource "aws_secretsmanager_secret" "github_token" {
  name                    = "${local.secret_prefix}/github-token"
  description             = "GitHub personal access token (contents:read scope) for alert context collection."
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "github_token" {
  secret_id     = aws_secretsmanager_secret.github_token.id
  secret_string = "placeholder"  # Update in Secrets Manager to enable GitHub context collection
}

resource "aws_secretsmanager_secret" "claude_api_key" {
  name                    = "${local.secret_prefix}/claude-api-key"
  description             = "Anthropic Claude API key for cloud AI analysis."
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "claude_api_key" {
  secret_id     = aws_secretsmanager_secret.claude_api_key.id
  secret_string = "placeholder"  # Update in Secrets Manager to enable Claude AI backend
}

resource "aws_secretsmanager_secret" "license_key" {
  name                    = "${local.secret_prefix}/license-key"
  description             = "Wachd SMB or Enterprise license key."
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "license_key" {
  secret_id     = aws_secretsmanager_secret.license_key.id
  secret_string = "placeholder"  # Update in Secrets Manager with your license key
}

resource "aws_secretsmanager_secret" "smtp_credentials" {
  name                    = "${local.secret_prefix}/smtp-credentials"
  description             = "SMTP credentials for outbound email. JSON with 'username' and 'password' keys. Works with any SMTP provider (Resend, SendGrid, SES, Mailgun, etc.)."
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "smtp_credentials" {
  secret_id     = aws_secretsmanager_secret.smtp_credentials.id
  secret_string = jsonencode({ username = "placeholder", password = "placeholder" })  # Update in Secrets Manager to enable email notifications
}

# ============================================================================
# IAM Role for External Secrets Operator (IRSA)
# ============================================================================

resource "aws_iam_role" "eso" {
  name = "role-eso-wachd-${var.environment}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = aws_iam_openid_connect_provider.eks.arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          # ESO service account in the external-secrets namespace
          "${replace(aws_iam_openid_connect_provider.eks.url, "https://", "")}:sub" = "system:serviceaccount:external-secrets:external-secrets"
          "${replace(aws_iam_openid_connect_provider.eks.url, "https://", "")}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })
}

resource "aws_iam_policy" "eso" {
  name        = "policy-eso-wachd-${var.environment}"
  description = "Allow ESO to read Wachd secrets from Secrets Manager"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "secretsmanager:GetSecretValue",
        "secretsmanager:DescribeSecret",
      ]
      # Scoped to only wachd secrets in this environment — principle of least privilege
      Resource = "arn:aws:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:${local.secret_prefix}/*"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "eso" {
  role       = aws_iam_role.eso.name
  policy_arn = aws_iam_policy.eso.arn
}

# ============================================================================
# Wachd Namespace
# ============================================================================

resource "kubernetes_namespace" "wachd" {
  metadata {
    name = "wachd"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [aws_eks_node_group.wachd]
}

# ============================================================================
# External Secrets Operator — Helm Install
# ============================================================================

resource "helm_release" "external_secrets" {
  name             = "external-secrets"
  repository       = "https://charts.external-secrets.io"
  chart            = "external-secrets"
  version          = "0.10.3"
  namespace        = "external-secrets"
  create_namespace = true
  wait             = true   # Wait for ESO pods + CRDs to be ready before kubectl_manifest runs

  set {
    name  = "serviceAccount.name"
    value = "external-secrets"
  }

  # Annotate the ESO service account with the IRSA role ARN
  # This lets ESO assume the role and read from Secrets Manager without any long-lived credentials
  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = aws_iam_role.eso.arn
  }

  depends_on = [
    aws_eks_node_group.wachd,
    aws_iam_role_policy_attachment.eso,
  ]
}

# ============================================================================
# ClusterSecretStore — connects ESO to AWS Secrets Manager
# ============================================================================

resource "kubectl_manifest" "cluster_secret_store" {
  yaml_body = yamlencode({
    apiVersion = "external-secrets.io/v1beta1"
    kind       = "ClusterSecretStore"
    metadata = {
      name = "aws-secrets-manager"
    }
    spec = {
      provider = {
        aws = {
          service = "SecretsManager"
          region  = var.region
          auth = {
            # Use IRSA via the ESO service account — no static credentials
            jwt = {
              serviceAccountRef = {
                name      = "external-secrets"
                namespace = "external-secrets"
              }
            }
          }
        }
      }
    }
  })

  depends_on = [helm_release.external_secrets]
}

# ============================================================================
# ExternalSecret resources
# Each maps one Secrets Manager path → one K8s secret in the wachd namespace.
# DevOps engineers update the value in Secrets Manager; ESO syncs automatically.
# ============================================================================

locals {
  external_secrets = {
    "wachd-db-secret" = {
      secretKey = "password"
      smKey     = "${local.secret_prefix}/postgres-password"
    }
    "wachd-redis-secret" = {
      secretKey = "password"
      smKey     = "${local.secret_prefix}/redis-auth-token"
    }
    "wachd-encryption-key" = {
      secretKey = "encryption-key"
      smKey     = "${local.secret_prefix}/encryption-key"
    }
    "wachd-oidc-secret" = {
      secretKey = "client-secret"
      smKey     = "${local.secret_prefix}/oidc-client-secret"
    }
    "wachd-slack-webhook" = {
      secretKey = "url"
      smKey     = "${local.secret_prefix}/slack-webhook-url"
    }
    "wachd-github-token" = {
      secretKey = "token"
      smKey     = "${local.secret_prefix}/github-token"
    }
    "wachd-claude-key" = {
      secretKey = "api-key"
      smKey     = "${local.secret_prefix}/claude-api-key"
    }
    "wachd-license" = {
      secretKey = "license-key"
      smKey     = "${local.secret_prefix}/license-key"
    }
  }
}

# wachd-smtp-creds needs two keys (username + password). Credentials are stored
# as a JSON object in Secrets Manager; ESO's dataFrom.extract splits them into
# individual keys. Works with any SMTP provider — just update the SM secret.
resource "kubectl_manifest" "smtp_creds_secret" {
  yaml_body = yamlencode({
    apiVersion = "external-secrets.io/v1beta1"
    kind       = "ExternalSecret"
    metadata = {
      name      = "wachd-smtp-creds"
      namespace = "wachd"
    }
    spec = {
      refreshInterval = "1h"
      secretStoreRef = {
        name = "aws-secrets-manager"
        kind = "ClusterSecretStore"
      }
      target = {
        name           = "wachd-smtp-creds"
        creationPolicy = "Owner"
      }
      dataFrom = [{
        extract = {
          key = "${local.secret_prefix}/smtp-credentials"
        }
      }]
    }
  })

  depends_on = [
    kubectl_manifest.cluster_secret_store,
    kubernetes_namespace.wachd,
  ]
}

resource "kubectl_manifest" "external_secrets" {
  for_each = local.external_secrets

  yaml_body = yamlencode({
    apiVersion = "external-secrets.io/v1beta1"
    kind       = "ExternalSecret"
    metadata = {
      name      = each.key
      namespace = "wachd"
    }
    spec = {
      refreshInterval = "1h"
      secretStoreRef = {
        name = "aws-secrets-manager"
        kind = "ClusterSecretStore"
      }
      target = {
        name           = each.key
        creationPolicy = "Owner"
      }
      data = [{
        secretKey = each.value.secretKey
        remoteRef = {
          key = each.value.smKey
        }
      }]
    }
  })

  depends_on = [
    kubectl_manifest.cluster_secret_store,
    kubernetes_namespace.wachd,
  ]
}
