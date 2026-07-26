variable "project_name" {
  description = "Project name prefix"
  type        = string
  default     = "coffeeshop"
}

variable "environment" {
  description = "Isolation label; the CI root accepts only ci"
  type        = string
  default     = "ci"
}

variable "aws_region" {
  description = "AWS Region for the dedicated CI plane"
  type        = string
  default     = "ap-southeast-1"
}

variable "expected_aws_account_id" {
  description = "Explicit account boundary for CI resources"
  type        = string
  validation {
    condition     = can(regex("^[0-9]{12}$", var.expected_aws_account_id))
    error_message = "expected_aws_account_id must contain 12 digits."
  }
}

variable "github_repository" {
  description = "GitHub owner/repository allowed to assume the candidate build role"
  type        = string
  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository))
    error_message = "github_repository must use owner/repository form."
  }
}

variable "github_oidc_provider_arn" {
  description = "Existing account-level GitHub Actions OIDC provider ARN"
  type        = string
  validation {
    condition     = can(regex("^arn:aws:iam::[0-9]{12}:oidc-provider/token\\.actions\\.githubusercontent\\.com$", var.github_oidc_provider_arn))
    error_message = "github_oidc_provider_arn must identify the GitHub Actions provider."
  }
}

variable "candidate_repository_prefix" {
  description = "Only ECR repositories with this prefix are writable by CI build identity"
  type        = string
  default     = "coffeeshop-candidate-"
  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9._/-]*-$", var.candidate_repository_prefix))
    error_message = "candidate_repository_prefix must be a lowercase ECR prefix ending in a dash."
  }
}

variable "cluster_name" {
  description = "Optional dedicated K3s cluster/instance name"
  type        = string
  default     = null
}

variable "vpc_cidr" {
  description = "CIDR for the isolated CI VPC"
  type        = string
  default     = "10.2.0.0/16"
  validation {
    condition     = can(cidrnetmask(var.vpc_cidr))
    error_message = "vpc_cidr must be a valid IPv4 CIDR."
  }
}

variable "public_subnet_cidr" {
  description = "Single public subnet CIDR; CI intentionally has no NAT Gateway"
  type        = string
  default     = "10.2.1.0/24"
  validation {
    condition     = can(cidrnetmask(var.public_subnet_cidr))
    error_message = "public_subnet_cidr must be a valid IPv4 CIDR."
  }
}

variable "availability_zone" {
  description = "Availability Zone for the single-node runner plane"
  type        = string
  default     = "ap-southeast-1a"
}

variable "operator_ssh_cidrs" {
  description = "Reviewed operator CIDRs allowed to SSH; empty uses SSM-only access"
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for cidr in var.operator_ssh_cidrs : can(cidrnetmask(cidr)) && cidr != "0.0.0.0/0"])
    error_message = "operator_ssh_cidrs must be valid and cannot contain 0.0.0.0/0."
  }
}

variable "key_name" {
  description = "Optional EC2 key pair; required only for SSH-based Ansible"
  type        = string
  default     = null
}

variable "ami_id" {
  description = "Optional pinned Ubuntu AMI; null resolves current Canonical Ubuntu 24.04"
  type        = string
  default     = null
}

variable "instance_type" {
  description = "On-Demand runner host instance type"
  type        = string
  default     = "t3.large"
}

variable "root_volume_size" {
  description = "Disposable encrypted gp3 root volume size in GiB"
  type        = number
  default     = 40
  validation {
    condition     = var.root_volume_size >= 20
    error_message = "root_volume_size must be at least 20 GiB."
  }
}

variable "root_volume_iops" {
  description = "gp3 root volume IOPS"
  type        = number
  default     = 3000
}

variable "root_volume_throughput" {
  description = "gp3 root volume throughput in MiB/s"
  type        = number
  default     = 125
}

variable "additional_tags" {
  description = "Additional CI-only tags"
  type        = map(string)
  default     = {}
}
