# ==============================================================================
# GitHub Actions OIDC Provider & Delivery Role
# ==============================================================================

# GitHub OIDC provider is account-global. Create it for a fresh account or accept an
# explicit existing ARN so Terraform never guesses ownership/import behavior.
resource "aws_iam_openid_connect_provider" "github" {
  count = var.github_oidc_provider_arn == null ? 1 : 0

  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
  tags           = local.common_tags
}

locals {
  github_oidc_provider_arn = coalesce(
    var.github_oidc_provider_arn,
    try(aws_iam_openid_connect_provider.github[0].arn, null)
  )
}

resource "aws_iam_role" "github_delivery_role" {
  name        = "${var.project_name}-prod-github-delivery-role"
  description = "IAM Role assumed by GitHub Actions for PROD deployment (OIDC)"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Federated = local.github_oidc_provider_arn
        }
        Action = "sts:AssumeRoleWithWebIdentity"
        Condition = {
          StringEquals = {
            "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          }
          StringLike = {
            "token.actions.githubusercontent.com:sub" = "repo:${var.github_repository}:environment:prod"
          }
        }
      }
    ]
  })

  tags = local.common_tags
}

# Policy for GitHub Delivery Role: ECR Push/Pull & EKS DescribeCluster
resource "aws_iam_policy" "github_delivery_policy" {
  name        = "${var.project_name}-prod-github-delivery-policy"
  description = "Scoped policy for GitHub Actions CI/CD deployment"

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
          "ecr:BatchGetImage",
          "ecr:DescribeImages",
          "ecr:PutImage",
          "ecr:InitiateLayerUpload",
          "ecr:UploadLayerPart",
          "ecr:CompleteLayerUpload"
        ]
        Resource = concat(
          [for repository in aws_ecr_repository.app : repository.arn],
          [for repository in aws_ecr_repository.candidate : repository.arn]
        )
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "github_delivery_attach" {
  role       = aws_iam_role.github_delivery_role.name
  policy_arn = aws_iam_policy.github_delivery_policy.arn
}

# Emergency delivery has the same repository-scoped ECR capability but a
# separate GitHub Environment trust boundary. This lets repository owners require
# a different reviewer set without granting the standard lane emergency identity.
resource "aws_iam_role" "github_emergency_delivery_role" {
  name        = "${var.project_name}-prod-github-emergency-delivery-role"
  description = "IAM Role assumed only by the GitHub PROD emergency Environment"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Federated = local.github_oidc_provider_arn
        }
        Action = "sts:AssumeRoleWithWebIdentity"
        Condition = {
          StringEquals = {
            "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          }
          StringLike = {
            "token.actions.githubusercontent.com:sub" = "repo:${var.github_repository}:environment:prod-emergency"
          }
        }
      }
    ]
  })

  tags = merge(local.common_tags, { DeliveryLane = "emergency" })
}

resource "aws_iam_role_policy_attachment" "github_emergency_delivery_attach" {
  role       = aws_iam_role.github_emergency_delivery_role.name
  policy_arn = aws_iam_policy.github_delivery_policy.arn
}

# DEV delivery runs in a separate GitHub Environment and may only read retained
# candidate repositories. It cannot write PROD repositories or mutate EKS.
resource "aws_iam_role" "github_dev_candidate_reader_role" {
  name        = "${var.project_name}-dev-candidate-reader-role"
  description = "Read-only candidate ECR identity used by the DEV delivery workflow"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = local.github_oidc_provider_arn
      }
      Action = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          "token.actions.githubusercontent.com:sub" = "repo:${var.github_repository}:environment:dev"
        }
      }
    }]
  })

  tags = merge(local.common_tags, { DeliveryLane = "dev-candidate-read" })
}

resource "aws_iam_policy" "github_dev_candidate_reader_policy" {
  name        = "${var.project_name}-dev-candidate-reader-policy"
  description = "Read-only access to retained immutable candidate repositories"

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
          "ecr:BatchGetImage",
          "ecr:DescribeImages",
          "ecr:GetDownloadUrlForLayer"
        ]
        Resource = [for repository in aws_ecr_repository.candidate : repository.arn]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "github_dev_candidate_reader_attach" {
  role       = aws_iam_role.github_dev_candidate_reader_role.name
  policy_arn = aws_iam_policy.github_dev_candidate_reader_policy.arn
}
