output "instance_id" {
  description = "Dedicated CI runner host instance ID"
  value       = module.runner_host.instance_id
}

output "public_ip" {
  description = "Public operator/bootstrap endpoint"
  value       = module.runner_host.public_ip
}

output "private_ip" {
  description = "Private K3s node address"
  value       = module.runner_host.private_ip
}

output "vpc_id" {
  description = "Isolated CI VPC ID"
  value       = aws_vpc.ci.id
}

output "cluster_name" {
  description = "Logical CI K3s cluster name"
  value       = local.cluster_name
}

output "candidate_build_role_arn" {
  description = "OIDC role for candidate ECR writes; it has no PROD deployment permission"
  value       = aws_iam_role.build.arn
}
