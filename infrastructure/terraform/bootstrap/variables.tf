variable "aws_region" {
  description = "AWS Region to deploy bootstrap resources"
  type        = string
  default     = "ap-southeast-1"
}

variable "project_name" {
  description = "Project name used as prefix for resources"
  type        = string
  default     = "coffeeshop"
}

variable "custom_bucket_name" {
  description = "Optional custom name for the S3 bucket. If not provided, a default name using project_name and aws_account_id will be generated."
  type        = string
  default     = null
}

variable "additional_tags" {
  description = "A map of additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}

variable "allowed_principals" {
  description = "List of IAM Principal ARNs allowed to assume the backend role (e.g., specific IAM Users, CI/CD Roles). Defaults to root account if null."
  type        = list(string)
  default     = null
}
