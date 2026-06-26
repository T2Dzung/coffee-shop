locals {
  # Allow the current AWS Account / IAM User running Terraform to assume the role
  allowed_principals = var.allowed_principals != null ? var.allowed_principals : ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"]
}

# IAM Role that Terraform will assume to manage state files
resource "aws_iam_role" "backend_role" {
  name        = "${var.project_name}-terraform-backend-role"
  description = "IAM Role assumed by Terraform to read/write state files securely"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          AWS = local.allowed_principals
        }
      }
    ]
  })

  tags = local.common_tags
}

# IAM Policy defining access to S3 Bucket and KMS Key
resource "aws_iam_policy" "backend_policy" {
  name        = "${var.project_name}-terraform-backend-policy"
  description = "Grants permissions to access S3 state bucket and KMS key"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # S3 Permissions: List bucket and read/write state objects
      {
        Effect = "Allow"
        Action = [
          "s3:ListBucket"
        ]
        Resource = [aws_s3_bucket.terraform_state.arn]
      },
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject"
        ]
        Resource = ["${aws_s3_bucket.terraform_state.arn}/*"]
      },
      # KMS Permissions: Decrypt and encrypt state files
      {
        Effect = "Allow"
        Action = [
          "kms:DescribeKey",
          "kms:Encrypt",
          "kms:Decrypt",
          "kms:GenerateDataKey"
        ]
        Resource = [aws_kms_key.state_key.arn]
      }
    ]
  })
}

# Attach the policy to the backend execution role
resource "aws_iam_role_policy_attachment" "backend_attach" {
  role       = aws_iam_role.backend_role.name
  policy_arn = aws_iam_policy.backend_policy.arn
}
