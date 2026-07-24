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

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.1.0.0/16"
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for public subnets (at least 2 AZs)"
  type        = list(string)
  default     = ["10.1.1.0/24", "10.1.2.0/24"]

  validation {
    condition     = length(var.public_subnet_cidrs) >= 2 && alltrue([for cidr in var.public_subnet_cidrs : can(cidrnetmask(cidr))])
    error_message = "public_subnet_cidrs must contain at least two valid CIDR blocks."
  }
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for private subnets (at least 2 AZs)"
  type        = list(string)
  default     = ["10.1.10.0/24", "10.1.20.0/24"]

  validation {
    condition     = length(var.private_subnet_cidrs) >= 2 && alltrue([for cidr in var.private_subnet_cidrs : can(cidrnetmask(cidr))])
    error_message = "private_subnet_cidrs must contain at least two valid CIDR blocks."
  }
}

variable "availability_zones" {
  description = "Availability zones for subnet placement"
  type        = list(string)
  default     = ["ap-southeast-1a", "ap-southeast-1b"]

  validation {
    condition     = length(var.availability_zones) >= 2 && length(distinct(var.availability_zones)) == length(var.availability_zones)
    error_message = "availability_zones must contain at least two distinct zones."
  }
}

check "subnet_list_lengths" {
  assert {
    condition = (
      length(var.public_subnet_cidrs) == length(var.private_subnet_cidrs) &&
      length(var.public_subnet_cidrs) == length(var.availability_zones)
    )
    error_message = "Public CIDRs, private CIDRs, and Availability Zones must have identical lengths."
  }
}

variable "cluster_name" {
  description = "Name of the EKS cluster for load balancer discovery tagging"
  type        = string
  default     = "coffeeshop-prod-eks"
}

variable "tags" {
  description = "Map of tags to apply to all resources"
  type        = map(string)
  default     = {}
}
