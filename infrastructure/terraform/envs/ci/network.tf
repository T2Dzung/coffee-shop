resource "aws_vpc" "ci" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = merge(local.common_tags, { Name = "${local.cluster_name}-vpc" })
}

resource "aws_internet_gateway" "ci" {
  vpc_id = aws_vpc.ci.id
  tags   = merge(local.common_tags, { Name = "${local.cluster_name}-igw" })
}

resource "aws_subnet" "ci" {
  vpc_id                  = aws_vpc.ci.id
  cidr_block              = var.public_subnet_cidr
  availability_zone       = var.availability_zone
  map_public_ip_on_launch = true
  tags                    = merge(local.common_tags, { Name = "${local.cluster_name}-public" })
}

resource "aws_route_table" "ci" {
  vpc_id = aws_vpc.ci.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.ci.id
  }

  tags = merge(local.common_tags, { Name = "${local.cluster_name}-public-rt" })
}

resource "aws_route_table_association" "ci" {
  subnet_id      = aws_subnet.ci.id
  route_table_id = aws_route_table.ci.id
}

resource "aws_security_group" "runner" {
  name        = "${local.cluster_name}-runner-sg"
  description = "Dedicated CI runner host: operator SSH ingress and outbound build traffic"
  vpc_id      = aws_vpc.ci.id
  tags        = merge(local.common_tags, { Name = "${local.cluster_name}-runner-sg" })
}

resource "aws_vpc_security_group_ingress_rule" "ssh" {
  for_each          = toset(var.operator_ssh_cidrs)
  security_group_id = aws_security_group.runner.id
  description       = "SSH from reviewed operator CIDR"
  from_port         = 22
  to_port           = 22
  ip_protocol       = "tcp"
  cidr_ipv4         = each.value
}

resource "aws_vpc_security_group_egress_rule" "outbound" {
  security_group_id = aws_security_group.runner.id
  description       = "Outbound GitHub, registry, package and OS update traffic"
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}
