output "config" {
  value = module.backend.config
}

output "ci_config" {
  value = module.backend.additional_configs["ci"]
}

output "dev_config" {
  value = module.backend.additional_configs["dev"]
}

output "retained_data_bucket_name" {
  description = "SSE-S3 bucket used for retained recovery data that is not active Terraform state."
  value       = aws_s3_bucket.retained_data.id
}
