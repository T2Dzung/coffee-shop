provider "aws" {
  region = var.aws_region
}

# Fetch the default VPC details to resolve VPC ID and default subnet
data "aws_vpc" "default" {
  default = true
}

# ==============================================================================
# Security Group for Dev Kubernetes HA Cluster
# ==============================================================================
resource "aws_security_group" "dev_sg" {
  name        = "${var.project_name}-${var.environment}-sg"
  description = "Security group for Multi-Node HA Dev Kubernetes cluster (k3s)"
  vpc_id      = data.aws_vpc.default.id

  # SSH access restricted to your public IP
  ingress {
    description = "Secure SSH access"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.my_ip]
  }

  # HTTP/HTTPS for Nginx Ingress Controller routing application traffic
  ingress {
    description = "HTTP Ingress"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "HTTPS Ingress"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Kubernetes API Server port to manage cluster via kubectl from local machine
  ingress {
    description = "Kubernetes API Server"
    from_port   = 6443
    to_port     = 6443
    protocol    = "tcp"
    cidr_blocks = [var.my_ip]
  }

  # Allow all internal traffic within the cluster for etcd, flannel, and kubelet communication
  ingress {
    description = "Internal cluster traffic"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    self        = true
  }

  # Egress: Allow all outbound connections
  egress {
    from_port        = 0
    to_port          = 0
    protocol         = "-1"
    cidr_blocks      = ["0.0.0.0/0"]
    ipv6_cidr_blocks = ["::/0"]
  }

  tags = {
    Name        = "${var.project_name}-${var.environment}-sg"
    Environment = var.environment
  }
}

# ==============================================================================
# IAM Role and Instance Profile for Dev Nodes (OIDC Alternative for EC2 k3s)
# ==============================================================================
resource "aws_iam_role" "dev_node_role" {
  name = "${var.project_name}-${var.environment}-node-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action    = "sts:AssumeRole"
        Effect    = "Allow"
        Principal = { Service = "ec2.amazonaws.com" }
      }
    ]
  })

  tags = {
    Environment = var.environment
  }
}

# ECR Policy: Allow pushing and pulling Docker images (needed for CI build and ECR cache)
resource "aws_iam_role_policy_attachment" "ecr_power_user" {
  role       = aws_iam_role.dev_node_role.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryPowerUser"
}

# SSM Core Policy: Enterprise best-practice to allow secure console access via AWS SSM
resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.dev_node_role.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "dev_profile" {
  name = "${var.project_name}-${var.environment}-instance-profile"
  role = aws_iam_role.dev_node_role.name
}

# ==============================================================================
# Primary K3s Control Plane Node (HA Seed Node)
# ==============================================================================
module "dev_k8s_primary" {
  source = "../../modules/ec2-instance"

  project_name         = "${var.project_name}-primary"
  environment          = var.environment
  instance_type        = var.primary_instance_type
  key_name             = var.key_name
  disk_size            = var.disk_size
  security_group_ids   = [aws_security_group.dev_sg.id]
  iam_instance_profile = aws_iam_instance_profile.dev_profile.name

  # Run as On-Demand to prevent unexpected Spot shutdowns during development
  enable_spot = false

  # Minimal Bootstrap: Prepare instance for Ansible configuration management
  user_data = <<-EOF
              #!/bin/bash
              set -e
              echo "=== STARTING PRIMARY DEV BOOTSTRAP ===" >> /var/log/user-data-done.log
              apt-get update -y
              apt-get install -y apt-transport-https ca-certificates curl gnupg lsb-release jq git unzip python3 python3-pip
              echo "=== PRIMARY DEV BOOTSTRAP COMPLETED: $(date) ===" >> /var/log/user-data-done.log
              EOF

  additional_tags = {
    K8sRole = "primary"
  }
}

# ==============================================================================
# Secondary K3s Control Plane Nodes (etcd HA Quorum Members)
# ==============================================================================
module "dev_k8s_secondary" {
  source = "../../modules/ec2-instance"
  count  = var.secondary_node_count

  project_name         = "${var.project_name}-secondary-${count.index + 1}"
  environment          = var.environment
  instance_type        = var.secondary_instance_type
  key_name             = var.key_name
  disk_size            = var.disk_size
  security_group_ids   = [aws_security_group.dev_sg.id]
  iam_instance_profile = aws_iam_instance_profile.dev_profile.name

  # Run as On-Demand to prevent unexpected Spot shutdowns during development
  enable_spot = false

  # Minimal Bootstrap: Prepare instance for Ansible configuration management
  user_data = <<-EOF
              #!/bin/bash
              set -e
              echo "=== STARTING SECONDARY DEV BOOTSTRAP ===" >> /var/log/user-data-done.log
              apt-get update -y
              apt-get install -y apt-transport-https ca-certificates curl gnupg lsb-release jq git unzip python3 python3-pip
              echo "=== SECONDARY DEV BOOTSTRAP COMPLETED: $(date) ===" >> /var/log/user-data-done.log
              EOF

  additional_tags = {
    K8sRole = "secondary"
  }
}

# ==============================================================================
# Ansible Configuration Management Trigger
# ==============================================================================
resource "terraform_data" "trigger_ansible" {
  depends_on = [
    aws_eip.primary_eip,
    module.dev_k8s_secondary
  ]

  provisioner "local-exec" {
    command     = "ansible-playbook -i inventory/aws_ec2.yml playbooks/site.yml --private-key=\"${var.private_key_path}\""
    working_dir = "${path.module}/../../ansible"
  }
}
