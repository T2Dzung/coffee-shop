# ==============================================================================
# S3 Bucket and IAM Credentials for PostgreSQL Backups
# ==============================================================================

data "aws_caller_identity" "current" {}

locals {
  aws_account_id              = data.aws_caller_identity.current.account_id
  postgres_backup_bucket_name = "${var.project_name}-${var.environment}-postgres-backup-${local.aws_account_id}"
}

resource "aws_s3_bucket" "postgres_backup" {
  bucket = local.postgres_backup_bucket_name

  lifecycle {
    prevent_destroy = true
  }

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-postgres-backup"
  })
}

resource "aws_s3_bucket_versioning" "postgres_backup" {
  bucket = aws_s3_bucket.postgres_backup.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "postgres_backup" {
  bucket = aws_s3_bucket.postgres_backup.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "postgres_backup" {
  bucket = aws_s3_bucket.postgres_backup.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "postgres_backup" {
  bucket = aws_s3_bucket.postgres_backup.id

  rule {
    id     = "postgres-backup-retention-safety"
    status = "Enabled"

    filter {}

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }

    noncurrent_version_expiration {
      noncurrent_days = 30
    }
  }
}

# IAM User for CNPG S3 Barman backup access
resource "aws_iam_user" "postgres_backup" {
  name = "${var.project_name}-${var.environment}-postgres-backup-user"
  tags = local.common_tags
}

resource "aws_iam_access_key" "postgres_backup" {
  user = aws_iam_user.postgres_backup.name
}

# IAM Policy for S3 access
resource "aws_iam_policy" "postgres_backup" {
  name        = "${var.project_name}-${var.environment}-postgres-backup-policy"
  description = "IAM policy for CloudNativePG backup user to access S3 backup bucket"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:GetBucketLocation",
          "s3:ListBucket",
          "s3:ListBucketMultipartUploads"
        ]
        Resource = aws_s3_bucket.postgres_backup.arn
      },
      {
        Effect = "Allow"
        Action = [
          "s3:AbortMultipartUpload",
          "s3:DeleteObject",
          "s3:GetObject",
          "s3:ListMultipartUploadParts",
          "s3:PutObject"
        ]
        Resource = "${aws_s3_bucket.postgres_backup.arn}/*"
      }
    ]
  })
}

resource "aws_iam_user_policy_attachment" "postgres_backup" {
  user       = aws_iam_user.postgres_backup.name
  policy_arn = aws_iam_policy.postgres_backup.arn
}
