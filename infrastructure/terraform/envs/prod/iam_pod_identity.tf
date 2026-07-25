# ==============================================================================
# IAM Roles & EKS Pod Identity Associations for Platform Workloads
# ==============================================================================

# ------------------------------------------------------------------------------
# 1. External Secrets Operator (ESO) Pod Identity Role & Association
# ------------------------------------------------------------------------------

resource "aws_iam_role" "eso_role" {
  name        = "${var.project_name}-${var.environment}-eso-role"
  description = "EKS Pod Identity IAM Role for External Secrets Operator"

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

resource "aws_iam_policy" "eso_policy" {
  name        = "${var.project_name}-${var.environment}-eso-policy"
  description = "Policy allowing ESO controller to read exact CoffeeShop secrets"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret"
        ]
        Resource = [
          aws_secretsmanager_secret.coffeeshop_app_secret.arn,
          aws_db_instance.postgres.master_user_secret[0].secret_arn
        ]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "eso_attach" {
  role       = aws_iam_role.eso_role.name
  policy_arn = aws_iam_policy.eso_policy.arn
}

resource "aws_eks_pod_identity_association" "eso" {
  cluster_name    = module.eks_cluster.cluster_name
  namespace       = "external-secrets"
  service_account = "external-secrets-sa"
  role_arn        = aws_iam_role.eso_role.arn

  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# 2. CloudWatch Observability Agent Pod Identity Role & Association
# ------------------------------------------------------------------------------

resource "aws_iam_role" "cloudwatch_agent_role" {
  name        = "${var.project_name}-${var.environment}-cloudwatch-agent-role"
  description = "EKS Pod Identity IAM Role for CloudWatch Observability Agent"

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

resource "aws_iam_role_policy_attachment" "cloudwatch_agent_attach" {
  role       = aws_iam_role.cloudwatch_agent_role.name
  policy_arn = "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy"
}

resource "aws_eks_pod_identity_association" "cloudwatch_agent" {
  cluster_name    = module.eks_cluster.cluster_name
  namespace       = "amazon-cloudwatch"
  service_account = "cloudwatch-agent"
  role_arn        = aws_iam_role.cloudwatch_agent_role.arn

  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# 3. EBS CSI Driver Pod Identity Role & Association
# ------------------------------------------------------------------------------

resource "aws_iam_role" "ebs_csi_role" {
  name        = "${var.project_name}-${var.environment}-ebs-csi-role"
  description = "EKS Pod Identity IAM Role for AWS EBS CSI Driver"

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

resource "aws_iam_role_policy_attachment" "ebs_csi_attach" {
  role       = aws_iam_role.ebs_csi_role.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

resource "aws_eks_pod_identity_association" "ebs_csi" {
  cluster_name    = module.eks_cluster.cluster_name
  namespace       = "kube-system"
  service_account = "ebs-csi-controller-sa"
  role_arn        = aws_iam_role.ebs_csi_role.arn

  tags = local.common_tags
}
