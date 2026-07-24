# ==============================================================================
# AWS Load Balancer Controller IAM Role & Pod Identity for PROD ALB ingress
# ==============================================================================

resource "aws_iam_role" "aws_lb_controller" {
  name        = "${var.project_name}-prod-aws-lb-controller-role"
  description = "IAM Role for AWS Load Balancer Controller via EKS Pod Identity"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "pods.eks.amazonaws.com"
        }
        Action = [
          "sts:AssumeRole",
          "sts:TagSession"
        ]
      }
    ]
  })

  tags = local.common_tags
}

# Policy allowing AWS Load Balancer Controller to manage ALBs, NLBs, and Target Groups
resource "aws_iam_policy" "aws_lb_controller_policy" {
  name        = "${var.project_name}-prod-aws-lb-controller-policy"
  description = "Official policy pinned to AWS Load Balancer Controller ${var.aws_load_balancer_controller_version}"
  policy      = file("${path.module}/policies/aws-load-balancer-controller-${var.aws_load_balancer_controller_version}.json")

  tags = local.common_tags
}

resource "aws_iam_role_policy_attachment" "aws_lb_controller_attach" {
  role       = aws_iam_role.aws_lb_controller.name
  policy_arn = aws_iam_policy.aws_lb_controller_policy.arn
}

resource "aws_eks_pod_identity_association" "aws_lb_controller" {
  cluster_name    = module.eks_cluster.cluster_name
  namespace       = "kube-system"
  service_account = "aws-load-balancer-controller"
  role_arn        = aws_iam_role.aws_lb_controller.arn

  tags = local.common_tags
}
