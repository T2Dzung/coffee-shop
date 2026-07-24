# Fetch the latest official Ubuntu Server 24.04 LTS gp3 AMI from Canonical
data "aws_ami" "ubuntu_24_04" {
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  owners = ["099720109477"] # Canonical's official AWS Account ID
}

# Invoke the official AWS EC2 Community Module
module "ec2" {
  source  = "terraform-aws-modules/ec2-instance/aws"
  version = "5.8.0"

  name = "${var.project_name}-${var.environment}-instance"

  ami                         = var.ami_id != null ? var.ami_id : data.aws_ami.ubuntu_24_04.id
  instance_type               = var.instance_type
  key_name                    = var.key_name
  subnet_id                   = var.subnet_id
  vpc_security_group_ids      = var.security_group_ids
  iam_instance_profile        = var.iam_instance_profile
  user_data_replace_on_change = true
  user_data                   = var.user_data
  associate_public_ip_address = var.associate_public_ip_address

  metadata_options = {
    http_endpoint               = "enabled"
    http_tokens                 = "required" # IMDSv2
    http_put_response_hop_limit = 1          # Restrict IAM credentials to the node only
  }

  # Dynamic Spot / On-Demand configuration managed elegantly by the community module
  create_spot_instance                = var.enable_spot
  spot_price                          = var.spot_price
  spot_type                           = var.spot_type
  spot_instance_interruption_behavior = var.spot_instance_interruption_behavior

  # The community module's broad volume_tags setting also tries to manage
  # separately attached EBS volumes. Keep root and Longhorn tag ownership
  # disjoint: root tags live in root_block_device; dedicated data volumes are
  # managed by their own aws_ebs_volume resources.
  enable_volume_tags = false

  # Root block device configuration with encryption enabled
  root_block_device = [
    {
      encrypted   = var.volume_encrypted
      volume_type = var.volume_type
      volume_size = var.disk_size
      iops        = var.root_volume_iops
      throughput  = var.root_volume_throughput
      tags = merge(
        {
          Environment = var.environment
          ManagedBy   = "Terraform"
        },
        var.additional_tags
      )
    }
  ]

  tags = merge(
    {
      Environment = var.environment
      ManagedBy   = "Terraform"
    },
    var.additional_tags
  )
}
