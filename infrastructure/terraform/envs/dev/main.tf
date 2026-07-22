provider "aws" {
  region = var.aws_region
}

# Single AMI lookup shared by all instances. When pinned_ami_id is set,
# the lookup still runs but resolved_ami_id uses the pinned value.
data "aws_ami" "ubuntu_24_04" {
  most_recent = true
  owners      = ["099720109477"] # Canonical

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

locals {
  # Pin AMI from input or fall back to latest lookup
  resolved_ami_id = coalesce(var.pinned_ami_id, data.aws_ami.ubuntu_24_04.id)

  # Keep fallback endpoint sizing configurable while retaining safe lab defaults.
  resolved_haproxy_instance_type    = coalesce(var.haproxy_instance_type, "t3.micro")
  resolved_haproxy_root_volume_size = coalesce(var.haproxy_root_volume_size, 20)

  effective_node_count   = var.dev_runtime_enabled ? var.node_count : 0
  create_haproxy_runtime = var.dev_runtime_enabled && var.create_haproxy_api_endpoint
  create_api_nlb_runtime = var.dev_runtime_enabled && var.create_nlb_api_endpoint

  common_tags = {
    Environment = var.environment
    Project     = var.project_name
    Cluster     = var.cluster_name
    ManagedBy   = "Terraform"
  }

  haproxy_public_ip        = try(aws_eip.haproxy[0].public_ip, null)
  haproxy_private_ip       = try(aws_instance.haproxy[0].private_ip, null)
  api_nlb_dns_name         = try(aws_lb.api_nlb[0].dns_name, null)
  api_nlb_target_group_arn = try(aws_lb_target_group.k3s_api[0].arn, null)
  haproxy_instance_id      = try(aws_instance.haproxy[0].id, null)

  k3s_registration_endpoint = var.active_api_endpoint_provider == "haproxy" ? local.haproxy_private_ip : local.api_nlb_dns_name
  active_api_endpoint       = var.active_api_endpoint_provider == "haproxy" ? local.haproxy_public_ip : local.api_nlb_dns_name

  k3s_tls_sans = compact([
    local.haproxy_public_ip,
    local.haproxy_private_ip,
    local.api_nlb_dns_name
  ])
}
