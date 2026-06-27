variable "project_name" {
  description = "The project name used for resource naming and tagging"
  type        = string
}

variable "environment" {
  description = "The target environment name (e.g., dev, staging, prod, shared)"
  type        = string
}

variable "instance_type" {
  description = "EC2 instance type to launch"
  type        = string
}

variable "subnet_id" {
  description = "The VPC Subnet ID to launch the instance in. If null, defaults to default VPC subnets."
  type        = string
  default     = null
}

variable "security_group_ids" {
  description = "List of Security Group IDs to associate with the instance"
  type        = list(string)
}

variable "key_name" {
  description = "Name of the AWS Key Pair to associate with the instance"
  type        = string
}

variable "disk_size" {
  description = "Size of the root EBS volume in GB"
  type        = number
  default     = 25
}

variable "user_data" {
  description = "The bootstrap user data script to run on instance start"
  type        = string
  default     = null
}

variable "iam_instance_profile" {
  description = "The IAM Instance Profile name to associate with the instance"
  type        = string
  default     = null
}

variable "enable_spot" {
  description = "If true, deploy as a Spot instance; otherwise deploy as On-Demand"
  type        = bool
  default     = false
}

variable "spot_price" {
  description = "Maximum hourly price limit for the Spot instance request"
  type        = string
  default     = "0.05"
}

variable "additional_tags" {
  description = "A map of additional tags to apply to the instance"
  type        = map(string)
  default     = {}
}

variable "volume_type" {
  description = "The type of EBS volume to use (e.g., gp3, gp2, io2)"
  type        = string
  default     = "gp3"
}

variable "volume_encrypted" {
  description = "Whether to encrypt the root EBS volume"
  type        = bool
  default     = true
}

variable "spot_type" {
  description = "The type of Spot instance request (e.g., persistent, one-time)"
  type        = string
  default     = "persistent"
}

variable "spot_instance_interruption_behavior" {
  description = "The behavior when a Spot instance is interrupted (e.g., stop, terminate)"
  type        = string
  default     = "stop"
}
