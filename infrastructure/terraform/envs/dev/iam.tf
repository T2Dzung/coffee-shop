# ==============================================================================
# IAM Role and Instance Profile for Dev Nodes
# ==============================================================================

resource "aws_iam_role" "dev_node_role" {
  name = "${var.cluster_name}-node-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action    = "sts:AssumeRole"
        Effect    = "Allow"
        Principal = { Service = "ec2.amazonaws.com" }
      }
    ]
  })

  tags = local.common_tags
}

# SSM Core Policy: Enterprise best-practice to allow secure console access via AWS SSM
resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.dev_node_role.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# Scoped ECR inline policy replacing AmazonEC2ContainerRegistryPowerUser
resource "aws_iam_role_policy" "ecr_scoped" {
  name = "${var.cluster_name}-ecr-scoped"
  role = aws_iam_role.dev_node_role.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = "ecr:GetAuthorizationToken"
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:GetDownloadUrlForLayer",
          "ecr:GetRepositoryPolicy",
          "ecr:DescribeRepositories",
          "ecr:ListImages",
          "ecr:DescribeImages",
          "ecr:BatchGetImage"
        ]
        Resource = [for repo in aws_ecr_repository.services : repo.arn]
      }
    ]
  })
}

resource "aws_iam_instance_profile" "dev_profile" {
  name = "${var.cluster_name}-instance-profile"
  role = aws_iam_role.dev_node_role.name
}
