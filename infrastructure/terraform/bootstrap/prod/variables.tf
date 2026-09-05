variable "aws_region" {
  type = string
}

variable "expected_aws_account_id" {
  type = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.expected_aws_account_id))
    error_message = "expected_aws_account_id must be a 12-digit AWS account ID."
  }
}

variable "project_name" {
  type    = string
  default = "coffeeshop"
}

variable "custom_bucket_name" {
  type    = string
  default = null
}

variable "additional_tags" {
  type    = map(string)
  default = {}
}

variable "allowed_principals" {
  type    = list(string)
  default = null
}

variable "state_encryption_mode" {
  type    = string
  default = "sse-kms"

  validation {
    condition     = contains(["sse-kms", "sse-s3"], var.state_encryption_mode)
    error_message = "state_encryption_mode must be sse-kms or sse-s3."
  }
}

variable "retained_data_bucket_name" {
  type    = string
  default = null

  validation {
    condition     = var.retained_data_bucket_name == null || can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.retained_data_bucket_name))
    error_message = "retained_data_bucket_name must be null or a valid S3 bucket name."
  }
}

variable "retained_data_abort_incomplete_multipart_days" {
  type    = number
  default = 7

  validation {
    condition     = var.retained_data_abort_incomplete_multipart_days >= 1
    error_message = "retained_data_abort_incomplete_multipart_days must be at least 1."
  }
}

variable "retained_data_noncurrent_version_expiration_days" {
  type    = number
  default = 30

  validation {
    condition     = var.retained_data_noncurrent_version_expiration_days >= 1
    error_message = "retained_data_noncurrent_version_expiration_days must be at least 1."
  }
}
