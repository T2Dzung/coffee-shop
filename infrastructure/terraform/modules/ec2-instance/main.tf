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
  version = "~> 5.0"

  name = "${var.project_name}-${var.environment}-instance"

  ami                         = data.aws_ami.ubuntu_24_04.id
  instance_type               = var.instance_type
  key_name                    = var.key_name
  subnet_id                   = var.subnet_id
  vpc_security_group_ids      = var.security_group_ids
  iam_instance_profile        = var.iam_instance_profile
  user_data_replace_on_change = true
  user_data                   = var.user_data

  # Dynamic Spot / On-Demand configuration managed elegantly by the community module
  create_spot_instance                = var.enable_spot
  spot_price                          = var.spot_price
  spot_type                           = var.spot_type
  spot_instance_interruption_behavior = var.spot_instance_interruption_behavior

  # Root block device configuration with encryption enabled
  root_block_device = [
    {
      encrypted   = var.volume_encrypted
      volume_type = var.volume_type
      volume_size = var.disk_size
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
