variable "project_name" {
  description = "Project name prefix for resource naming"
  type        = string
  default     = "coffeeshop"
}

variable "environment" {
  description = "Target environment name"
  type        = string
  default     = "prod"
}

variable "aws_region" {
  description = "AWS Region to deploy resources"
  type        = string
  default     = "ap-southeast-1"
}

variable "expected_aws_account_id" {
  description = "Required 12-digit AWS account ID allowed to own PROD resources"
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.expected_aws_account_id))
    error_message = "expected_aws_account_id must be a valid 12-digit AWS account ID."
  }
}

variable "cluster_name" {
  description = "Name of the EKS cluster"
  type        = string
  default     = "coffeeshop-prod-eks"
}

variable "cluster_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  default     = "1.35"
}

variable "vpc_cidr" {
  description = "CIDR block for the production VPC"
  type        = string
  default     = "10.1.0.0/16"
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for PROD public subnets"
  type        = list(string)
  default     = ["10.1.1.0/24", "10.1.2.0/24"]

  validation {
    condition     = length(var.public_subnet_cidrs) == 2 && alltrue([for cidr in var.public_subnet_cidrs : can(cidrnetmask(cidr))])
    error_message = "public_subnet_cidrs must contain exactly two valid CIDR blocks."
  }
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for PROD private node subnets"
  type        = list(string)
  default     = ["10.1.10.0/24", "10.1.20.0/24"]

  validation {
    condition     = length(var.private_subnet_cidrs) == 2 && alltrue([for cidr in var.private_subnet_cidrs : can(cidrnetmask(cidr))])
    error_message = "private_subnet_cidrs must contain exactly two valid CIDR blocks."
  }
}

variable "availability_zones" {
  description = "Exactly two Availability Zones used by the PROD foundation"
  type        = list(string)
  default     = ["ap-southeast-1a", "ap-southeast-1b"]

  validation {
    condition     = length(var.availability_zones) == 2 && length(distinct(var.availability_zones)) == 2
    error_message = "availability_zones must contain exactly two distinct zones."
  }
}

variable "cluster_endpoint_public_access_cidrs" {
  description = "Reviewed administrator CIDRs allowed to reach the public EKS API endpoint"
  type        = list(string)

  validation {
    condition = (
      length(var.cluster_endpoint_public_access_cidrs) > 0 &&
      alltrue([for cidr in var.cluster_endpoint_public_access_cidrs : can(cidrnetmask(cidr)) && cidr != "0.0.0.0/0"])
    )
    error_message = "Provide at least one valid administrator CIDR; 0.0.0.0/0 is forbidden for PROD."
  }
}

variable "node_disk_size" {
  description = "Root disk size in GiB for worker nodes (parameterized gp3)"
  type        = number
  default     = 20

  validation {
    condition     = var.node_disk_size >= 20
    error_message = "node_disk_size must be at least 20 GiB for the reviewed EKS node baseline."
  }
}

variable "node_disk_iops" {
  description = "gp3 IOPS for worker node root volumes"
  type        = number
  default     = 3000

  validation {
    condition     = var.node_disk_iops >= 3000 && var.node_disk_iops <= 16000
    error_message = "node_disk_iops must be within the gp3 range 3000-16000."
  }
}

variable "node_disk_throughput" {
  description = "gp3 throughput in MiB/s for worker node root volumes"
  type        = number
  default     = 125

  validation {
    condition     = var.node_disk_throughput >= 125 && var.node_disk_throughput <= 1000
    error_message = "node_disk_throughput must be within the gp3 range 125-1000 MiB/s."
  }
}

variable "node_instance_types" {
  description = "List of EC2 instance types for the worker node group"
  type        = list(string)
  default     = ["t3.medium"]
}

variable "node_desired_size" {
  description = "Desired managed-node count used by Terraform and hourly cost estimation"
  type        = number
  default     = 2

  validation {
    condition     = var.node_desired_size >= 1 && var.node_desired_size <= 3
    error_message = "node_desired_size must stay within the reviewed PROD-1 range 1-3."
  }
}

variable "budget_limit_amount" {
  description = "Monthly spending limit for AWS Budget alert in USD (Raw Gross Usage before credits)"
  type        = string
  default     = "200.0"
}

variable "budget_subscriber_email" {
  description = "Optional email address to receive AWS Budget threshold notifications"
  type        = string
  default     = null
}

variable "github_repository" {
  description = "GitHub repository (owner/repo) for OIDC trust boundary"
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository))
    error_message = "github_repository must use the owner/repository form."
  }
}

variable "github_oidc_provider_arn" {
  description = "Existing account-level GitHub OIDC provider ARN; null creates it in this state"
  type        = string
  default     = null

  validation {
    condition = (
      var.github_oidc_provider_arn == null ||
      can(regex("^arn:aws:iam::[0-9]{12}:oidc-provider/token\\.actions\\.githubusercontent\\.com$", var.github_oidc_provider_arn))
    )
    error_message = "github_oidc_provider_arn must be the account GitHub Actions OIDC provider ARN."
  }
}

variable "aws_load_balancer_controller_version" {
  description = "Pinned AWS Load Balancer Controller version whose official IAM policy is vendored"
  type        = string
  default     = "v3.4.2"

  validation {
    condition     = var.aws_load_balancer_controller_version == "v3.4.2"
    error_message = "Update the vendored official IAM policy and validation hash before changing controller version."
  }
}

variable "additional_tags" {
  description = "A map of additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}
