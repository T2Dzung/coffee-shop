variable "cluster_name" {
  description = "Name of the EKS cluster"
  type        = string
  default     = "coffeeshop-prod-eks"
}

variable "cluster_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  default     = "1.31"
}

variable "subnet_ids" {
  description = "List of subnet IDs for EKS control plane ENIs"
  type        = list(string)
}

variable "enabled_cluster_log_types" {
  description = "List of EKS control plane log streams to enable"
  type        = list(string)
  default     = ["api", "audit", "authenticator"]
}

variable "log_retention_in_days" {
  description = "CloudWatch log retention in days for EKS control plane logs"
  type        = number
  default     = 7
}

variable "endpoint_public_access_cidrs" {
  description = "Administrator CIDRs allowed to reach the public EKS API endpoint"
  type        = list(string)

  validation {
    condition = (
      length(var.endpoint_public_access_cidrs) > 0 &&
      alltrue([for cidr in var.endpoint_public_access_cidrs : can(cidrnetmask(cidr)) && cidr != "0.0.0.0/0"])
    )
    error_message = "At least one valid restricted public access CIDR is required; 0.0.0.0/0 is forbidden."
  }
}

variable "tags" {
  description = "Map of tags to apply to all resources"
  type        = map(string)
  default     = {}
}
