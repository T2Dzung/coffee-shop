variable "project_name" {
  description = "Project prefix for backend resources"
  type        = string
  default     = "coffeeshop"
}

variable "environment" {
  description = "Environment owning this account-local backend"
  type        = string
  validation {
    condition     = contains(["dev", "prod"], var.environment)
    error_message = "environment must be dev or prod."
  }
}

variable "custom_bucket_name" {
  description = "Optional globally unique state bucket name"
  type        = string
  default     = null
}

variable "additional_tags" {
  description = "Additional backend resource tags"
  type        = map(string)
  default     = {}
}

variable "allowed_principals" {
  description = "IAM principals allowed to assume the backend role"
  type        = list(string)
  default     = null
}
