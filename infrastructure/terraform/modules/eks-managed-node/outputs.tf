output "node_group_id" {
  description = "The ID of the EKS Managed Node Group"
  value       = aws_eks_node_group.this.id
}

output "node_group_name" {
  description = "Name of the managed node group"
  value       = aws_eks_node_group.this.node_group_name
}

output "node_group_arn" {
  description = "The ARN of the EKS Managed Node Group"
  value       = aws_eks_node_group.this.arn
}

output "node_role_arn" {
  description = "The ARN of the IAM Role used by worker nodes"
  value       = aws_iam_role.node.arn
}

output "node_role_name" {
  description = "The name of the IAM Role used by worker nodes"
  value       = aws_iam_role.node.name
}
