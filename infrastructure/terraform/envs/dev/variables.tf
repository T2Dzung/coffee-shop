variable "project_name" {
  description = "The project namespace to use for unique resource naming"
  type        = string
  default     = "coffeeshop"
}

variable "environment" {
  description = "The target environment name"
  type        = string
  default     = "dev"
}

variable "aws_region" {
  description = "AWS Region to deploy resources"
  type        = string
  default     = "ap-southeast-1"
}

variable "expected_aws_account_id" {
  description = "Explicit AWS account boundary for the DEV environment"
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.expected_aws_account_id))
    error_message = "expected_aws_account_id must contain 12 digits."
  }
}

variable "cluster_name" {
  description = "Name of the K3s cluster"
  type        = string
  default     = "coffeeshop-dev"
}

variable "github_repository" {
  description = "GitHub repository (owner/repo) whose protected dev/prod Environments may assume delivery roles"
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository))
    error_message = "github_repository must use the owner/repository form."
  }
}

variable "vpc_cidr" {
  description = "CIDR block for the dedicated VPC"
  type        = string
  default     = "10.20.0.0/16"
}

variable "availability_zones" {
  description = "List of Availability Zones to use"
  type        = list(string)
  default     = ["ap-southeast-1a", "ap-southeast-1b", "ap-southeast-1c"]
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for the public subnets"
  type        = list(string)
  default     = ["10.20.10.0/24", "10.20.20.0/24", "10.20.30.0/24"]
}

variable "admin_cidrs" {
  description = "List of CIDRs allowed to access API and SSH"
  type        = list(string)
}

variable "application_ingress_cidrs" {
  description = "List of CIDRs allowed to access the application via HTTP/HTTPS. If null, falls back to admin_cidrs."
  type        = list(string)
  default     = null
}

variable "node_count" {
  description = "Number of K3s servers (must be odd and >= 3)"
  type        = number
  default     = 3
  validation {
    condition     = var.node_count >= 3 && var.node_count % 2 != 0
    error_message = "Node count must be an odd number and at least 3."
  }
}

variable "dev_runtime_enabled" {
  description = "Create billable DEV runtime resources (K3s nodes, dedicated Longhorn EBS, and the selected API endpoint). Keep false while DEV is decommissioned."
  type        = bool
  default     = false
}

variable "k3s_instance_type" {
  description = "EC2 instance type for K3s servers"
  type        = string
  default     = "t3.large"
}

variable "k3s_root_volume_size" {
  description = "Size of root volume in GiB"
  type        = number
  default     = 30

  validation {
    condition     = var.k3s_root_volume_size >= 20
    error_message = "k3s_root_volume_size must be at least 20 GiB."
  }
}

variable "k3s_root_volume_iops" {
  description = "IOPS for the root gp3 volume"
  type        = number
  default     = 3000
}

variable "k3s_root_volume_throughput" {
  description = "Throughput for the root gp3 volume in MiB/s"
  type        = number
  default     = 125
}

variable "api_nlb_internal" {
  description = "Whether the API NLB is internal"
  type        = bool
  default     = false
}

variable "api_nlb_deletion_protection" {
  description = "Enable deletion protection on the API NLB"
  type        = bool
  default     = true
}

variable "key_name" {
  description = "AWS key pair name for SSH access"
  type        = string
}

variable "create_haproxy_api_endpoint" {
  description = "Create HAProxy EC2 fallback endpoint"
  type        = bool
  default     = true
}

variable "create_nlb_api_endpoint" {
  description = "Create AWS NLB endpoint"
  type        = bool
  default     = false
}

variable "active_api_endpoint_provider" {
  description = "Active endpoint provider: haproxy or nlb"
  type        = string
  default     = "haproxy"
  validation {
    condition     = contains(["haproxy", "nlb"], var.active_api_endpoint_provider)
    error_message = "Must be either 'haproxy' or 'nlb'."
  }
}

variable "pinned_ami_id" {
  description = "Pinned Ubuntu AMI ID. When set, all instances use this AMI instead of looking up most_recent independently."
  type        = string
  default     = null
}

variable "haproxy_instance_type" {
  description = "EC2 instance type for the temporary HAProxy API endpoint. Null uses the safe dev default."
  type        = string
  default     = null
}

variable "haproxy_root_volume_size" {
  description = "Root volume size in GiB for the temporary HAProxy API endpoint. Null uses the safe dev default."
  type        = number
  default     = null

  validation {
    condition     = var.haproxy_root_volume_size == null || var.haproxy_root_volume_size >= 8
    error_message = "haproxy_root_volume_size must be null or at least 8 GiB."
  }
}

variable "longhorn_data_volume_enabled" {
  description = "Enable dedicated EBS volume for Longhorn data"
  type        = bool
  default     = true
}

variable "longhorn_data_volume_size" {
  description = "Size of Longhorn dedicated volume in GiB"
  type        = number
  default     = 50

  validation {
    condition     = var.longhorn_data_volume_size >= 30
    error_message = "longhorn_data_volume_size must be at least 30 GiB. Existing EBS volumes cannot be shrunk in place."
  }
}

variable "longhorn_data_volume_type" {
  description = "Type of Longhorn dedicated volume"
  type        = string
  default     = "gp3"
}

variable "longhorn_data_volume_iops" {
  description = "IOPS for Longhorn dedicated gp3 volume"
  type        = number
  default     = 3000
}

variable "longhorn_data_volume_throughput" {
  description = "Throughput for Longhorn dedicated gp3 volume in MiB/s"
  type        = number
  default     = 125
}

variable "longhorn_data_device_name" {
  description = "Device name to attach the Longhorn volume to the EC2 instances"
  type        = string
  default     = "/dev/sdf"
}
