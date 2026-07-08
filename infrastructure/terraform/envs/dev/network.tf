# ==============================================================================
# Network resources for K3s 3-Server HA cluster
# ==============================================================================

resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-vpc"
  })
}

resource "aws_internet_gateway" "igw" {
  vpc_id = aws_vpc.main.id

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-igw"
  })
}

resource "aws_subnet" "public" {
  count                   = length(var.public_subnet_cidrs)
  vpc_id                  = aws_vpc.main.id
  cidr_block              = var.public_subnet_cidrs[count.index]
  availability_zone       = var.availability_zones[count.index]
  map_public_ip_on_launch = true

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-public-${var.availability_zones[count.index]}"
  })
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.igw.id
  }

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-public-rt"
  })
}

resource "aws_route_table_association" "public" {
  count          = length(var.public_subnet_cidrs)
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# ==============================================================================
# Security Groups
# ==============================================================================

# Security Group for K3s Nodes
resource "aws_security_group" "k3s_node_sg" {
  name        = "${var.cluster_name}-node-sg"
  description = "Security group for the K3s nodes"
  vpc_id      = aws_vpc.main.id

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-node-sg"
  })
}

resource "aws_vpc_security_group_ingress_rule" "node_ssh_ingress" {
  count             = length(var.admin_cidrs)
  security_group_id = aws_security_group.k3s_node_sg.id
  description       = "Allow SSH from admin CIDR"
  from_port         = 22
  to_port           = 22
  ip_protocol       = "tcp"
  cidr_ipv4         = var.admin_cidrs[count.index]
}

resource "aws_vpc_security_group_ingress_rule" "node_api_self" {
  security_group_id            = aws_security_group.k3s_node_sg.id
  description                  = "Server internal API fallback"
  from_port                    = 6443
  to_port                      = 6443
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.k3s_node_sg.id
}

resource "aws_vpc_security_group_ingress_rule" "node_etcd_self" {
  security_group_id            = aws_security_group.k3s_node_sg.id
  description                  = "etcd client/peer communication"
  from_port                    = 2379
  to_port                      = 2380
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.k3s_node_sg.id
}

resource "aws_vpc_security_group_ingress_rule" "node_kubelet_self" {
  security_group_id            = aws_security_group.k3s_node_sg.id
  description                  = "kubelet communication"
  from_port                    = 10250
  to_port                      = 10250
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.k3s_node_sg.id
}

resource "aws_vpc_security_group_ingress_rule" "node_flannel_self" {
  security_group_id            = aws_security_group.k3s_node_sg.id
  description                  = "Cilium VXLAN overlay"
  from_port                    = 8472
  to_port                      = 8472
  ip_protocol                  = "udp"
  referenced_security_group_id = aws_security_group.k3s_node_sg.id
}

# resource "aws_vpc_security_group_ingress_rule" "node_nfs_self" {
#   security_group_id            = aws_security_group.k3s_node_sg.id
#   description                  = "Transitional NFSv4 cache"
#   from_port                    = 2049
#   to_port                      = 2049
#   ip_protocol                  = "tcp"
#   referenced_security_group_id = aws_security_group.k3s_node_sg.id
# }

resource "aws_vpc_security_group_ingress_rule" "node_exporter_self" {
  security_group_id            = aws_security_group.k3s_node_sg.id
  description                  = "Prometheus Node Exporter metrics scraping"
  from_port                    = 9100
  to_port                      = 9100
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.k3s_node_sg.id
}

resource "aws_vpc_security_group_egress_rule" "node_egress_all_ipv4" {
  security_group_id = aws_security_group.k3s_node_sg.id
  description       = "Allow all outbound IPv4"
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "node_egress_all_ipv6" {
  security_group_id = aws_security_group.k3s_node_sg.id
  description       = "Allow all outbound IPv6"
  ip_protocol       = "-1"
  cidr_ipv6         = "::/0"
}
