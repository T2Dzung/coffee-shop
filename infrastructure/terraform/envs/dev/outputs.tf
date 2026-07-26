# ==============================================================================
# Development Environment Infrastructure Outputs (Phase 1)
# ==============================================================================

output "cluster_name" {
  description = "The name of the K3s cluster"
  value       = var.cluster_name
}

output "dev_runtime_enabled" {
  description = "Whether billable DEV runtime resources are part of the desired state"
  value       = var.dev_runtime_enabled
}

output "active_api_endpoint_provider" {
  description = "The active API endpoint provider"
  value       = var.active_api_endpoint_provider
}

output "active_api_endpoint" {
  description = "The public endpoint for API access"
  value       = local.active_api_endpoint
}

output "k3s_registration_endpoint" {
  description = "The endpoint used for node registration (join)"
  value       = local.k3s_registration_endpoint
}

output "k3s_tls_sans" {
  description = "List of all active/migration TLS SANs"
  value       = local.k3s_tls_sans
}

output "haproxy_public_ip" {
  description = "HAProxy EIP public IP"
  value       = local.haproxy_public_ip
}

output "haproxy_instance_id" {
  description = "HAProxy EC2 Instance ID"
  value       = local.haproxy_instance_id
}

output "api_nlb_dns_name" {
  description = "DNS name of the API NLB"
  value       = local.api_nlb_dns_name
}

output "api_nlb_target_group_arn" {
  description = "ARN of the API NLB target group"
  value       = local.api_nlb_target_group_arn
}

output "node_instance_ids" {
  description = "Instance IDs of the K3s nodes"
  value       = module.k3s_servers[*].instance_id
}

output "node_private_ips" {
  description = "Private IPs of the K3s nodes"
  value       = module.k3s_servers[*].private_ip
}

output "node_public_ips" {
  description = "Public IPs of the K3s nodes"
  value       = module.k3s_servers[*].public_ip
}

output "vpc_id" {
  description = "The ID of the dedicated VPC"
  value       = aws_vpc.main.id
}

output "public_subnet_ids" {
  description = "The IDs of the public subnets"
  value       = aws_subnet.public[*].id
}

output "availability_zones" {
  description = "The availability zones used"
  value       = var.availability_zones
}

output "ecr_repository_urls" {
  description = "The URLs of the ECR repositories for the microservices"
  value       = { for k, v in aws_ecr_repository.services : k => v.repository_url }
}

output "github_actions_role_arn" {
  description = "The ARN of the IAM role for GitHub Actions to assume via OIDC"
  value       = aws_iam_role.github_actions.arn
}

output "longhorn_data_volume_ids" {
  description = "EBS volume IDs allocated for Longhorn data"
  value       = aws_ebs_volume.longhorn[*].id
}

output "longhorn_data_volume_size" {
  description = "Configured size in GiB for each dedicated Longhorn EBS volume"
  value       = var.longhorn_data_volume_size
}

output "longhorn_data_mount_path" {
  description = "Path where Longhorn volumes will be mounted on the nodes"
  value       = "/var/lib/longhorn"
}

output "postgres_backup_bucket_name" {
  description = "Name of the S3 bucket for PostgreSQL backups"
  value       = aws_s3_bucket.postgres_backup.id
}

output "postgres_backup_iam_access_key_id" {
  description = "IAM access key ID for PostgreSQL S3 backups"
  value       = aws_iam_access_key.postgres_backup.id
}

output "postgres_backup_iam_secret_access_key" {
  description = "IAM secret access key for PostgreSQL S3 backups"
  value       = aws_iam_access_key.postgres_backup.secret
  sensitive   = true
}
