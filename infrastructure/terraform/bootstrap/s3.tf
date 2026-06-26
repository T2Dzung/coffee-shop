# ==============================================================================
# KMS Customer Managed Key (CMK) for State Encryption
# ==============================================================================

# Customer Managed Key (CMK) for encrypting Terraform state files in S3
resource "aws_kms_key" "state_key" {
  description             = "KMS key for encrypting Terraform state files in S3"
  deletion_window_in_days = 30
  enable_key_rotation     = true # Security best practice for automated key rotation compliance

  tags = merge(local.common_tags, {
    Name = "${var.project_name}-state-key"
  })
}

# Create a user-friendly alias for the KMS key to simplify references
resource "aws_kms_alias" "state_key_alias" {
  name          = "alias/${var.project_name}-state-key"
  target_key_id = aws_kms_key.state_key.key_id
}

# ==============================================================================
# S3 Bucket for Remote State Storage
# ==============================================================================

# S3 Bucket to store Terraform state files securely
resource "aws_s3_bucket" "terraform_state" {
  bucket        = local.bucket_name
  force_destroy = false

  # Critical safeguard: prevent accidental deletion of the state storage bucket
  lifecycle {
    prevent_destroy = true
  }

  tags = merge(local.common_tags, {
    Name = local.bucket_name
  })
}

# Enable versioning to allow recovery from accidental state overrides or corruption
resource "aws_s3_bucket_versioning" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id
  versioning_configuration {
    status = "Enabled"
  }
}

# Secure the bucket using KMS Customer Managed Key (SSE-KMS) instead of default SSE-S3
resource "aws_s3_bucket_server_side_encryption_configuration" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.state_key.arn
    }
  }
}

# Explicitly block all public access to the state bucket to prevent data exposure
resource "aws_s3_bucket_public_access_block" "terraform_state" {
  bucket                  = aws_s3_bucket.terraform_state.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
