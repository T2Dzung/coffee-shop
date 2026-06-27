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
# Generate a secure, unique token for K3s HA cluster join
# ==============================================================================
resource "random_password" "k3s_token" {
  length  = 48
  special = false
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

  # Bootstrap: Automate Docker, K3s (Primary with --cluster-init), and Helm
  user_data = <<-EOF
              #!/bin/bash
              set -e
              
              echo "=== STARTING PRIMARY DEV BOOTSTRAP ===" >> /var/log/user-data-done.log
              apt-get update -y && apt-get upgrade -y
              apt-get install -y apt-transport-https ca-certificates curl gnupg lsb-release jq git unzip
              
              # 1. Install Docker Engine (Needed by DinD sidecars)
              mkdir -p /etc/apt/keyrings
              curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
              chmod a+r /etc/apt/keyrings/docker.gpg
              echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu noble stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
              apt-get update -y
              apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
              usermod -aG docker ubuntu
              systemctl enable docker
              systemctl start docker

              # Get Public & Private IPs dynamically via IMDSv2 for K3s TLS SAN configuration
              IMDS_TOKEN=$(curl -s -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 21600")
              PUBLIC_IP=$(curl -s -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/meta-data/public-ipv4)
              PRIVATE_IP=$(curl -s -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/meta-data/local-ipv4)

              # 2. Install K3s as Primary Control Plane Node (embedded etcd initializer)
              # Configured with TLS SAN to allow secure kubectl access from local machine using public IP
              curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--cluster-init --token ${random_password.k3s_token.result} --tls-san $PUBLIC_IP --tls-san $PRIVATE_IP --disable traefik --write-kubeconfig-mode 644" sh -
              
              # 3. Configure kubectl for user 'ubuntu'
              mkdir -p /home/ubuntu/.kube
              cp /etc/rancher/k3s/k3s.yaml /home/ubuntu/.kube/config
              chown -R ubuntu:ubuntu /home/ubuntu/.kube
              chmod 600 /home/ubuntu/.kube/config
              echo 'export KUBECONFIG=/home/ubuntu/.kube/config' >> /home/ubuntu/.bashrc

              # 4. Install Helm v3
              curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3
              chmod 700 get_helm.sh
              ./get_helm.sh
              rm get_helm.sh

              echo "=== PRIMARY DEV BOOTSTRAP COMPLETED: $(date) ===" >> /var/log/user-data-done.log
              echo "Docker version: $(docker --version)" >> /var/log/user-data-done.log
              echo "K3s status: $(kubectl get nodes)" >> /var/log/user-data-done.log
              echo "Helm version: $(helm version --short)" >> /var/log/user-data-done.log
              EOF

  additional_tags = {
    K8sRole       = "primary"
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

  # Bootstrap: Automate Docker and K3s (Join as Server Node to form HA etcd cluster)
  user_data = <<-EOF
              #!/bin/bash
              set -e
              
              echo "=== STARTING SECONDARY DEV BOOTSTRAP ===" >> /var/log/user-data-done.log
              apt-get update -y && apt-get upgrade -y
              apt-get install -y apt-transport-https ca-certificates curl gnupg lsb-release jq git unzip
              
              # 1. Install Docker Engine (Needed by DinD sidecars)
              mkdir -p /etc/apt/keyrings
              curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
              chmod a+r /etc/apt/keyrings/docker.gpg
              echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu noble stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
              apt-get update -y
              apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
              usermod -aG docker ubuntu
              systemctl enable docker
              systemctl start docker

              # 2. Wait for Primary K3s API Server to be fully online at port 6443
              echo "Waiting for Primary K3s node (${module.dev_k8s_primary.private_ip}:6443) to be online..." >> /var/log/user-data-done.log
              while ! nc -z ${module.dev_k8s_primary.private_ip} 6443; do
                sleep 5
              done
              echo "Primary K3s node is online! Proceeding with K3s secondary join..." >> /var/log/user-data-done.log

              # 3. Install K3s as Secondary Server (Join the etcd HA cluster)
              curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--server https://${module.dev_k8s_primary.private_ip}:6443 --token ${random_password.k3s_token.result} --disable traefik --write-kubeconfig-mode 644" sh -
              
              # 4. Configure kubectl for user 'ubuntu'
              mkdir -p /home/ubuntu/.kube
              cp /etc/rancher/k3s/k3s.yaml /home/ubuntu/.kube/config
              chown -R ubuntu:ubuntu /home/ubuntu/.kube
              chmod 600 /home/ubuntu/.kube/config
              echo 'export KUBECONFIG=/home/ubuntu/.kube/config' >> /home/ubuntu/.bashrc

              # 5. Install Helm v3
              curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3
              chmod 700 get_helm.sh
              ./get_helm.sh
              rm get_helm.sh

              echo "=== SECONDARY DEV BOOTSTRAP COMPLETED: $(date) ===" >> /var/log/user-data-done.log
              echo "Docker version: $(docker --version)" >> /var/log/user-data-done.log
              echo "K3s status: $(kubectl get nodes)" >> /var/log/user-data-done.log
              echo "Helm version: $(helm version --short)" >> /var/log/user-data-done.log
              EOF

  additional_tags = {
    K8sRole       = "secondary"
  }
}
