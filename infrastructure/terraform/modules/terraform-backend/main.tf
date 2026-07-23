data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  bucket_name        = var.custom_bucket_name != null ? var.custom_bucket_name : "${var.project_name}-terraform-state-${data.aws_caller_identity.current.account_id}"
  allowed_principals = var.allowed_principals != null ? var.allowed_principals : ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"]
  common_tags = merge({
    Project     = var.project_name
    Environment = var.environment
    ManagedBy   = "Terraform"
    Role        = "IaC State Storage"
  }, var.additional_tags)
}

resource "aws_resourcegroups_group" "bootstrap_group" {
  name        = "${var.project_name}-backend-resources"
  description = "Groups S3 Bucket and KMS Key used for Terraform Remote State"

  resource_query {
    query = jsonencode({
      ResourceTypeFilters = ["AWS::S3::Bucket", "AWS::KMS::Key"]
      TagFilters = [{
        Key    = "Project"
        Values = [var.project_name]
      }]
    })
  }
  tags = local.common_tags
}

resource "aws_kms_key" "state_key" {
  description             = "KMS key for encrypting Terraform state files in S3"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  tags                    = merge(local.common_tags, { Name = "${var.project_name}-state-key" })
}

resource "aws_kms_alias" "state_key_alias" {
  name          = "alias/${var.project_name}-state-key"
  target_key_id = aws_kms_key.state_key.key_id
}

resource "aws_s3_bucket" "terraform_state" {
  bucket        = local.bucket_name
  force_destroy = false

  lifecycle {
    prevent_destroy = true
  }
  tags = merge(local.common_tags, { Name = local.bucket_name })
}

resource "aws_s3_bucket_versioning" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.state_key.arn
    }
  }
}

resource "aws_s3_bucket_public_access_block" "terraform_state" {
  bucket                  = aws_s3_bucket.terraform_state.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_iam_role" "backend_role" {
  name        = "${var.project_name}-terraform-backend-role"
  description = "IAM Role assumed by Terraform to read/write state files securely"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { AWS = local.allowed_principals }
    }]
  })
  tags = local.common_tags
}

resource "aws_iam_policy" "backend_policy" {
  name        = "${var.project_name}-terraform-backend-policy"
  description = "Grants permissions to access S3 state bucket and KMS key"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = [aws_s3_bucket.terraform_state.arn]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
        Resource = ["${aws_s3_bucket.terraform_state.arn}/*"]
      },
      {
        Effect   = "Allow"
        Action   = ["kms:DescribeKey", "kms:Encrypt", "kms:Decrypt", "kms:GenerateDataKey"]
        Resource = [aws_kms_key.state_key.arn]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "backend_attach" {
  role       = aws_iam_role.backend_role.name
  policy_arn = aws_iam_policy.backend_policy.arn
}
