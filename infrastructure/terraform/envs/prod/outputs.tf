output "vpc_id" {
  description = "The ID of the production VPC"
  value       = module.vpc.vpc_id
}

output "public_subnet_ids" {
  description = "List of public subnet IDs"
  value       = module.vpc.public_subnet_ids
}

output "private_subnet_ids" {
  description = "List of private subnet IDs"
  value       = module.vpc.private_subnet_ids
}

output "cluster_name" {
  description = "The name of the production EKS cluster"
  value       = module.eks_cluster.cluster_name
}

output "cluster_endpoint" {
  description = "The endpoint URL for the EKS API server"
  value       = module.eks_cluster.cluster_endpoint
}

output "cluster_arn" {
  description = "The exact ARN used to verify kubeconfig targets the reviewed cluster"
  value       = module.eks_cluster.cluster_arn
}

output "node_group_name" {
  description = "Managed node group name used by the G3 runtime gate"
  value       = module.eks_nodes.node_group_name
}

output "cluster_oidc_provider_arn" {
  description = "The ARN of the OIDC Provider for EKS IRSA/Pod Identity"
  value       = module.eks_cluster.cluster_oidc_provider_arn
}

output "github_delivery_role_arn" {
  description = "The ARN of the GitHub Actions OIDC delivery role"
  value       = aws_iam_role.github_delivery_role.arn
}

output "github_emergency_delivery_role_arn" {
  description = "The GitHub Actions OIDC role restricted to the PROD emergency Environment"
  value       = aws_iam_role.github_emergency_delivery_role.arn
}

output "github_dev_candidate_reader_role_arn" {
  description = "Read-only candidate ECR role for the GitHub DEV Environment"
  value       = aws_iam_role.github_dev_candidate_reader_role.arn
}

output "budget_name" {
  description = "The name of the AWS Budget"
  value       = aws_budgets_budget.prod_budget.name
}

output "ecr_repository_urls" {
  description = "Map of CoffeeShop ECR repository URLs"
  value       = { for k, v in aws_ecr_repository.app : k => v.repository_url }
}

output "candidate_ecr_repository_urls" {
  description = "Retained CI candidate repositories keyed by component"
  value       = { for k, v in aws_ecr_repository.candidate : k => v.repository_url }
}

output "aws_load_balancer_controller_role_arn" {
  description = "The IAM role ARN for AWS Load Balancer Controller EKS Pod Identity"
  value       = aws_iam_role.aws_lb_controller.arn
}

output "rds_endpoint" {
  description = "The connection endpoint for RDS PostgreSQL"
  value       = aws_db_instance.postgres.endpoint
}

output "rds_address" {
  description = "The hostname address for RDS PostgreSQL"
  value       = aws_db_instance.postgres.address
}

output "rds_port" {
  description = "The port for RDS PostgreSQL"
  value       = aws_db_instance.postgres.port
}

output "rds_master_secret_arn" {
  description = "The ARN of the Secrets Manager secret for RDS master user password"
  value       = aws_db_instance.postgres.master_user_secret[0].secret_arn
}

output "coffeeshop_app_secret_arn" {
  description = "The ARN of the AWS Secrets Manager secret for CoffeeShop app credentials"
  value       = aws_secretsmanager_secret.coffeeshop_app_secret.arn
}

output "eso_role_arn" {
  description = "The IAM role ARN for External Secrets Operator Pod Identity"
  value       = aws_iam_role.eso_role.arn
}

output "cloudwatch_agent_role_arn" {
  description = "The IAM role ARN for CloudWatch Observability Agent Pod Identity"
  value       = aws_iam_role.cloudwatch_agent_role.arn
}

output "ebs_csi_role_arn" {
  description = "The IAM role ARN for AWS EBS CSI Driver Pod Identity"
  value       = aws_iam_role.ebs_csi_role.arn
}

output "synthetics_artifact_bucket" {
  description = "Retained encrypted artifact bucket for the O2 Synthetics canary"
  value       = aws_s3_bucket.synthetics_artifacts.id
}

output "slo_canary_name" {
  description = "O2 golden-journey canary name when the bounded runtime is enabled"
  value       = try(aws_synthetics_canary.golden_journey[0].name, null)
}

output "slo_dashboard_name" {
  description = "O2 golden-journey dashboard name when the bounded runtime is enabled"
  value       = try(aws_cloudwatch_dashboard.golden_journey[0].dashboard_name, null)
}
