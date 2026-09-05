locals {
  retained_data_bucket_name = var.retained_data_bucket_name != null ? var.retained_data_bucket_name : "${var.project_name}-retained-data-${data.aws_caller_identity.current.account_id}"
  retained_data_tags = merge({
    Project     = var.project_name
    Environment = "prod"
    ManagedBy   = "Terraform"
    Role        = "Retained Data Archive"
  }, var.additional_tags)
}

resource "aws_s3_bucket" "retained_data" {
  bucket        = local.retained_data_bucket_name
  force_destroy = false

  lifecycle {
    prevent_destroy = true
  }

  tags = merge(local.retained_data_tags, { Name = local.retained_data_bucket_name })
}

resource "aws_s3_bucket_ownership_controls" "retained_data" {
  bucket = aws_s3_bucket.retained_data.id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_versioning" "retained_data" {
  bucket = aws_s3_bucket.retained_data.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "retained_data" {
  bucket = aws_s3_bucket.retained_data.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "retained_data" {
  bucket = aws_s3_bucket.retained_data.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "retained_data" {
  bucket = aws_s3_bucket.retained_data.id

  depends_on = [aws_s3_bucket_versioning.retained_data]

  rule {
    id     = "retained-data-version-safety"
    status = "Enabled"

    filter {}

    abort_incomplete_multipart_upload {
      days_after_initiation = var.retained_data_abort_incomplete_multipart_days
    }

    noncurrent_version_expiration {
      noncurrent_days = var.retained_data_noncurrent_version_expiration_days
    }
  }
}
