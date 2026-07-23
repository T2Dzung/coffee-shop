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
