variable "cluster_name" {
  description = "Name of the EKS cluster"
  type        = string
}

variable "node_group_name" {
  description = "Name of the EKS managed node group"
  type        = string
  default     = "system-nodes"
}

variable "subnet_ids" {
  description = "Subnet IDs where managed nodes will be placed (private subnets)"
  type        = list(string)
}

variable "instance_types" {
  description = "List of EC2 instance types for the node group"
  type        = list(string)
  default     = ["t3.medium"]
}

variable "capacity_type" {
  description = "Capacity type for the node group (ON_DEMAND or SPOT)"
  type        = string
  default     = "ON_DEMAND"
}

variable "desired_size" {
  description = "Desired number of worker nodes"
  type        = number
  default     = 2
}

variable "min_size" {
  description = "Minimum number of worker nodes"
  type        = number
  default     = 1
}

variable "max_size" {
  description = "Maximum number of worker nodes"
  type        = number
  default     = 3
}

variable "disk_size" {
  description = "Root disk size in GiB for worker nodes (parameterized gp3)"
  type        = number
  default     = 20
}

variable "volume_type" {
  description = "EBS volume type for worker node root volume"
  type        = string
  default     = "gp3"

  validation {
    condition     = var.volume_type == "gp3"
    error_message = "The PROD-1 managed-node storage contract requires gp3."
  }
}

variable "volume_iops" {
  description = "IOPS for the gp3 root volume"
  type        = number
  default     = 3000

  validation {
    condition     = var.volume_iops >= 3000 && var.volume_iops <= 16000
    error_message = "volume_iops must be within the gp3 range 3000-16000."
  }
}

variable "volume_throughput" {
  description = "Throughput in MiB/s for the gp3 root volume"
  type        = number
  default     = 125

  validation {
    condition     = var.volume_throughput >= 125 && var.volume_throughput <= 1000
    error_message = "volume_throughput must be within the gp3 range 125-1000 MiB/s."
  }
}

variable "tags" {
  description = "Map of tags to apply to all resources"
  type        = map(string)
  default     = {}
}
