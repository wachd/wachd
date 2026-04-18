# ============================================================================
# Outputs — used to configure Helm values and verify the deployment
# ============================================================================

output "aws_account_id" {
  description = "AWS account ID"
  value       = data.aws_caller_identity.current.account_id
}

output "aws_region" {
  description = "AWS region"
  value       = var.region
}

# ============================================================================
# EKS
# ============================================================================

output "eks_cluster_name" {
  description = "EKS cluster name"
  value       = aws_eks_cluster.wachd.name
}

output "eks_cluster_endpoint" {
  description = "EKS cluster API server endpoint"
  value       = aws_eks_cluster.wachd.endpoint
}

output "eks_get_credentials_command" {
  description = "Command to configure kubectl to connect to this cluster"
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${aws_eks_cluster.wachd.name}"
}

output "eks_oidc_provider_arn" {
  description = "EKS OIDC provider ARN (for IRSA)"
  value       = aws_iam_openid_connect_provider.eks.arn
}

# ============================================================================
# RDS PostgreSQL
# ============================================================================

output "postgres_host" {
  description = "RDS PostgreSQL hostname (use in Helm values)"
  value       = aws_db_instance.wachd.address
}

output "postgres_port" {
  description = "RDS PostgreSQL port"
  value       = aws_db_instance.wachd.port
}

output "postgres_database" {
  description = "PostgreSQL database name"
  value       = aws_db_instance.wachd.db_name
}

output "postgres_username" {
  description = "PostgreSQL admin username"
  value       = aws_db_instance.wachd.username
}

output "postgres_connection_string_template" {
  description = "PostgreSQL connection string template — password is in Secrets Manager and synced by ESO"
  value       = "postgresql://${aws_db_instance.wachd.username}@${aws_db_instance.wachd.address}:${aws_db_instance.wachd.port}/wachd?sslmode=require"
}

# ============================================================================
# ElastiCache Redis
# ============================================================================

output "redis_primary_endpoint" {
  description = "Redis primary endpoint hostname (use in Helm values)"
  value       = aws_elasticache_replication_group.wachd.primary_endpoint_address
}

output "redis_port" {
  description = "Redis port (6379 with TLS — use rediss:// scheme)"
  value       = 6379
}

output "redis_tls_note" {
  description = "Redis connection note"
  value       = "AWS ElastiCache uses port 6379 with TLS. Use scheme rediss:// in the Wachd Redis URL."
}

# ============================================================================
# Secrets Manager
# ============================================================================

output "secrets_manager_prefix" {
  description = "AWS Secrets Manager path prefix for all Wachd secrets"
  value       = local.secret_prefix
}

output "secrets_manager_console_url" {
  description = "AWS Console URL to view and update Wachd secrets"
  value       = "https://${var.region}.console.aws.amazon.com/secretsmanager/listsecrets?region=${var.region}"
}

output "secret_arns" {
  description = "ARNs of all Secrets Manager secrets (for IAM policy verification)"
  value = {
    postgres_password  = aws_secretsmanager_secret.postgres_password.arn
    redis_auth_token   = aws_secretsmanager_secret.redis_auth_token.arn
    encryption_key     = aws_secretsmanager_secret.encryption_key.arn
    oidc_client_secret = aws_secretsmanager_secret.oidc_client_secret.arn
    slack_webhook_url  = aws_secretsmanager_secret.slack_webhook_url.arn
    github_token       = aws_secretsmanager_secret.github_token.arn
    claude_api_key     = aws_secretsmanager_secret.claude_api_key.arn
    license_key        = aws_secretsmanager_secret.license_key.arn
  }
}

# ============================================================================
# External Secrets Operator (ESO)
# ============================================================================

output "eso_iam_role_arn" {
  description = "IAM role ARN for ESO IRSA (annotated on the ESO service account)"
  value       = aws_iam_role.eso.arn
}

# ============================================================================
# Amazon Managed Prometheus (AMP)
# ============================================================================

output "amp_workspace_id" {
  description = "Amazon Managed Prometheus workspace ID"
  value       = aws_prometheus_workspace.wachd.id
}

output "amp_remote_write_url" {
  description = "AMP remote write URL (configure in ADOT / Prometheus remote_write)"
  value       = "${aws_prometheus_workspace.wachd.prometheus_endpoint}api/v1/remote_write"
}

output "amp_query_url" {
  description = "AMP query URL (configure as Grafana data source)"
  value       = aws_prometheus_workspace.wachd.prometheus_endpoint
}

output "adot_iam_role_arn" {
  description = "IAM role ARN for ADOT collector IRSA (annotate adot-collector service account)"
  value       = aws_iam_role.adot.arn
}

# ============================================================================
# Amazon Managed Grafana (if created)
# ============================================================================

output "grafana_endpoint" {
  description = "Amazon Managed Grafana workspace URL (null if create_grafana=false)"
  value       = var.create_grafana ? aws_grafana_workspace.wachd[0].endpoint : null
}

# ============================================================================
# ECR (if created)
# ============================================================================

output "ecr_repository_url" {
  description = "ECR repository URL (null if create_ecr=false)"
  value       = var.create_ecr ? aws_ecr_repository.wachd[0].repository_url : null
}

output "ecr_login_command" {
  description = "Command to authenticate Docker to ECR (null if create_ecr=false)"
  value       = var.create_ecr ? "aws ecr get-login-password --region ${var.region} | docker login --username AWS --password-stdin ${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com" : null
}

# ============================================================================
# Cognito (if created)
# ============================================================================

output "cognito_user_pool_id" {
  description = "Cognito User Pool ID (null if create_cognito=false)"
  value       = var.create_cognito ? aws_cognito_user_pool.wachd[0].id : null
}

output "cognito_client_id" {
  description = "Cognito app client ID — use in Helm values auth.oidc.clientId (null if create_cognito=false)"
  value       = var.create_cognito ? aws_cognito_user_pool_client.wachd[0].id : null
}

output "cognito_issuer_url" {
  description = "Cognito OIDC issuer URL — use in Helm values auth.oidc.issuerUrl (null if create_cognito=false)"
  value       = var.create_cognito ? "https://cognito-idp.${var.region}.amazonaws.com/${aws_cognito_user_pool.wachd[0].id}" : null
}

# ============================================================================
# VPC
# ============================================================================

output "vpc_id" {
  description = "VPC ID"
  value       = aws_vpc.wachd.id
}

output "private_subnet_ids" {
  description = "Private subnet IDs (EKS nodes, RDS, Redis)"
  value       = aws_subnet.private[*].id
}

output "public_subnet_ids" {
  description = "Public subnet IDs (load balancers)"
  value       = aws_subnet.public[*].id
}

# ============================================================================
# Summary (non-sensitive — safe to print in CI logs)
# ============================================================================

output "deployment_summary" {
  description = "Non-sensitive deployment summary — safe to log in CI"
  value = {
    region              = var.region
    environment         = var.environment
    eks_cluster         = aws_eks_cluster.wachd.name
    postgres_host       = aws_db_instance.wachd.address
    redis_host          = aws_elasticache_replication_group.wachd.primary_endpoint_address
    redis_port          = "6379 (TLS — use rediss:// scheme)"
    secrets_prefix      = local.secret_prefix
    eso_role_arn        = aws_iam_role.eso.arn
    amp_workspace       = aws_prometheus_workspace.wachd.id
    grafana_url         = var.create_grafana ? aws_grafana_workspace.wachd[0].endpoint : "not created"
    ecr_url             = var.create_ecr ? aws_ecr_repository.wachd[0].repository_url : "not created (using ghcr.io)"
    cognito_pool_id     = var.create_cognito ? aws_cognito_user_pool.wachd[0].id : "not created"
    wachd_url           = "https://${var.wachd_hostname}"
    kubectl_cmd         = "aws eks update-kubeconfig --region ${var.region} --name ${aws_eks_cluster.wachd.name}"
    secrets_console_url = "https://${var.region}.console.aws.amazon.com/secretsmanager/listsecrets?region=${var.region}"
  }
}

# ============================================================================
# Sensitive outputs — not printed in CI, available via terraform output -raw
# ============================================================================

output "postgres_password" {
  description = "PostgreSQL admin password (also stored in Secrets Manager)"
  value       = random_password.postgres.result
  sensitive   = true
}

output "wachd_encryption_key" {
  description = "AES-256 hex encryption key (also stored in Secrets Manager)"
  value       = random_id.wachd_encryption_key.hex
  sensitive   = true
}
