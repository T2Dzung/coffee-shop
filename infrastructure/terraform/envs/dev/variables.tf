variable "project_name" {
  description = "The project namespace to use for unique resource naming"
  type        = string
  default     = "coffeeshop"
}

variable "environment" {
  description = "The target environment name (e.g., dev, staging, prod)"
  type        = string
  default     = "dev"
}

variable "aws_region" {
  description = "AWS Region to deploy resources"
  type        = string
  default     = "ap-southeast-1"
}

variable "primary_instance_type" {
  description = "EC2 instance type for the Primary K3s Control Plane Node"
  type        = string
  default     = "t3.large"
}

variable "secondary_instance_type" {
  description = "EC2 instance type for the Secondary K3s Worker Nodes"
  type        = string
  default     = "t3.large"
}

variable "disk_size" {
  description = "Size of the root EBS volume in GB"
  type        = number
  default     = 50
}

variable "key_name" {
  description = "Name of the AWS Key Pair to associate with the EC2 instance"
  type        = string
  default     = null
}

variable "private_key_path" {
  description = "Local absolute path to the private key (.pem) file for SSH command output"
  type        = string
  default     = null
}

variable "my_ip" {
  description = "Your public IP address in CIDR format to restrict SSH access"
  type        = string
  default     = null
}

variable "secondary_node_count" {
  description = "Number of secondary control plane nodes to join the HA cluster. Set to 2 for a standard 3-node etcd quorum."
  type        = number
  default     = 2
}

