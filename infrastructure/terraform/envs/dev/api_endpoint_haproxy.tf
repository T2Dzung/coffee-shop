# ==============================================================================
# HAProxy Load Balancer (Fallback for Dev)
# ==============================================================================

resource "aws_eip" "haproxy" {
  count  = local.create_haproxy_runtime ? 1 : 0
  domain = "vpc"
  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-haproxy-eip"
  })
}

resource "aws_eip_association" "haproxy" {
  count         = local.create_haproxy_runtime ? 1 : 0
  instance_id   = aws_instance.haproxy[0].id
  allocation_id = aws_eip.haproxy[0].id
}

resource "aws_security_group" "haproxy_sg" {
  count       = local.create_haproxy_runtime ? 1 : 0
  name        = "${var.cluster_name}-haproxy-sg"
  description = "Security group for K3s HAProxy endpoint"
  vpc_id      = aws_vpc.main.id

  # Admin SSH
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = var.admin_cidrs
  }

  # kubectl/API via EIP
  ingress {
    from_port   = 6443
    to_port     = 6443
    protocol    = "tcp"
    cidr_blocks = var.admin_cidrs
  }

  # HTTP Gateway via EIP
  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = var.application_ingress_cidrs != null ? var.application_ingress_cidrs : var.admin_cidrs
  }

  # K3s Node SG join via private IP
  ingress {
    from_port       = 6443
    to_port         = 6443
    protocol        = "tcp"
    security_groups = [aws_security_group.k3s_node_sg.id]
  }

  # Outbound API proxy and health check to nodes
  egress {
    from_port       = 6443
    to_port         = 6443
    protocol        = "tcp"
    security_groups = [aws_security_group.k3s_node_sg.id]
  }

  # Outbound HTTP Gateway proxy to nodes
  egress {
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.k3s_node_sg.id]
  }

  # Internet access for apt install (can be restricted later)
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-haproxy-sg"
  })
}

# IAM Role with SSM core (no ECR)
resource "aws_iam_role" "haproxy_role" {
  count = local.create_haproxy_runtime ? 1 : 0
  name  = "${var.cluster_name}-haproxy-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
      }
    ]
  })
  tags = local.common_tags
}

resource "aws_iam_role_policy_attachment" "haproxy_ssm" {
  count      = local.create_haproxy_runtime ? 1 : 0
  role       = aws_iam_role.haproxy_role[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "haproxy_profile" {
  count = local.create_haproxy_runtime ? 1 : 0
  name  = "${var.cluster_name}-haproxy-profile"
  role  = aws_iam_role.haproxy_role[0].name
  tags  = local.common_tags
}



resource "aws_instance" "haproxy" {
  count                  = local.create_haproxy_runtime ? 1 : 0
  ami                    = local.resolved_ami_id
  instance_type          = local.resolved_haproxy_instance_type
  subnet_id              = aws_subnet.public[0].id
  vpc_security_group_ids = [aws_security_group.haproxy_sg[0].id]
  key_name               = var.key_name
  iam_instance_profile   = aws_iam_instance_profile.haproxy_profile[0].name

  root_block_device {
    volume_size           = local.resolved_haproxy_root_volume_size
    volume_type           = "gp3"
    encrypted             = true
    delete_on_termination = true
  }

  user_data = <<-EOF
              #!/bin/bash
              set -e
              apt-get update -y
              apt-get install -y python3 curl
              EOF

  tags = merge(local.common_tags, {
    Name             = "${var.cluster_name}-haproxy"
    K8sRole          = "haproxy"
    EndpointProvider = "haproxy"
  })
}

resource "aws_vpc_security_group_ingress_rule" "node_api_haproxy_ingress" {
  count                        = local.create_haproxy_runtime ? 1 : 0
  security_group_id            = aws_security_group.k3s_node_sg.id
  description                  = "Allow API traffic from HAProxy"
  from_port                    = 6443
  to_port                      = 6443
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.haproxy_sg[0].id
}

resource "aws_vpc_security_group_ingress_rule" "node_http_haproxy_ingress" {
  count                        = local.create_haproxy_runtime ? 1 : 0
  security_group_id            = aws_security_group.k3s_node_sg.id
  description                  = "Allow HTTP Gateway traffic from HAProxy"
  from_port                    = 8080
  to_port                      = 8080
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.haproxy_sg[0].id
}
