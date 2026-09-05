output "config" {
  description = "S3 backend coordinates"
  value = {
    bucket       = aws_s3_bucket.terraform_state.bucket
    region       = data.aws_region.current.name
    encrypt      = true
    kms_key_id   = var.state_encryption_mode == "sse-kms" ? aws_kms_key.state_key[0].arn : null
    role_arn     = aws_iam_role.backend_role.arn
    use_lockfile = true
  }
}

output "additional_configs" {
  description = "Prefix-scoped backend coordinates for additional environments"
  value = {
    for key, role in aws_iam_role.additional_backend : key => {
      bucket       = aws_s3_bucket.terraform_state.bucket
      region       = data.aws_region.current.name
      encrypt      = true
      kms_key_id   = var.additional_backend_roles[key].kms_access && var.state_encryption_mode == "sse-kms" ? aws_kms_key.state_key[0].arn : null
      role_arn     = role.arn
      use_lockfile = true
    }
  }
}
