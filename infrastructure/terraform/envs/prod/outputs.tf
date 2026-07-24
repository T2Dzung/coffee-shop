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

output "budget_name" {
  description = "The name of the AWS Budget"
  value       = aws_budgets_budget.prod_budget.name
}

output "ecr_repository_urls" {
  description = "Map of CoffeeShop ECR repository URLs"
  value       = { for k, v in aws_ecr_repository.app : k => v.repository_url }
}

output "aws_load_balancer_controller_role_arn" {
  description = "The IAM role ARN for AWS Load Balancer Controller EKS Pod Identity"
  value       = aws_iam_role.aws_lb_controller.arn
}

