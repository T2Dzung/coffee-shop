output "config" {
  description = "S3 backend coordinates"
  value = {
    bucket       = aws_s3_bucket.terraform_state.bucket
    region       = data.aws_region.current.name
    encrypt      = true
    kms_key_id   = aws_kms_key.state_key.arn
    role_arn     = aws_iam_role.backend_role.arn
    use_lockfile = true
  }
}
