# ==============================================================================
# API Network Load Balancer (NLB) for K3s 6443 Endpoint
# ==============================================================================

resource "aws_security_group" "api_nlb_sg" {
  count       = local.create_api_nlb_runtime ? 1 : 0
  name        = "${var.cluster_name}-api-nlb-sg"
  description = "Security group for the K3s API NLB"
  vpc_id      = aws_vpc.main.id

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-api-nlb-sg"
  })
}

resource "aws_vpc_security_group_ingress_rule" "api_nlb_ingress" {
  count             = local.create_api_nlb_runtime ? 1 : 0
  security_group_id = aws_security_group.api_nlb_sg[0].id
  description       = "Allow K8s API traffic from admin CIDRs"
  from_port         = 6443
  to_port           = 6443
  ip_protocol       = "tcp"
  cidr_ipv4         = var.admin_cidrs[0]
}

resource "aws_vpc_security_group_ingress_rule" "api_nlb_ingress_all" {
  count             = local.create_api_nlb_runtime && length(var.admin_cidrs) > 1 ? length(var.admin_cidrs) - 1 : 0
  security_group_id = aws_security_group.api_nlb_sg[0].id
  description       = "Allow K8s API traffic from admin CIDR ${count.index + 1}"
  from_port         = 6443
  to_port           = 6443
  ip_protocol       = "tcp"
  cidr_ipv4         = var.admin_cidrs[count.index + 1]
}

resource "aws_vpc_security_group_egress_rule" "api_nlb_egress" {
  count                        = local.create_api_nlb_runtime ? 1 : 0
  security_group_id            = aws_security_group.api_nlb_sg[0].id
  description                  = "Allow traffic to K3s nodes"
  from_port                    = 6443
  to_port                      = 6443
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.k3s_node_sg.id
}

resource "aws_vpc_security_group_ingress_rule" "node_api_nlb_ingress" {
  count                        = local.create_api_nlb_runtime ? 1 : 0
  security_group_id            = aws_security_group.k3s_node_sg.id
  description                  = "Allow API traffic from NLB"
  from_port                    = 6443
  to_port                      = 6443
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.api_nlb_sg[0].id
}

resource "aws_lb" "api_nlb" {
  count              = local.create_api_nlb_runtime ? 1 : 0
  name               = "${var.cluster_name}-api-nlb"
  internal           = var.api_nlb_internal
  load_balancer_type = "network"
  subnets            = aws_subnet.public[*].id
  security_groups    = [aws_security_group.api_nlb_sg[0].id]

  enable_deletion_protection       = var.api_nlb_deletion_protection
  enable_cross_zone_load_balancing = true

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-api-nlb"
  })
}

resource "aws_lb_target_group" "k3s_api" {
  count       = local.create_api_nlb_runtime ? 1 : 0
  name        = "${var.cluster_name}-api-tg"
  port        = 6443
  protocol    = "TCP"
  vpc_id      = aws_vpc.main.id
  target_type = "instance"

  health_check {
    protocol            = "TCP"
    port                = "6443"
    interval            = 10
    healthy_threshold   = 2
    unhealthy_threshold = 2
    timeout             = 6
  }

  tags = local.common_tags
}

resource "aws_lb_listener" "k3s_api" {
  count             = local.create_api_nlb_runtime ? 1 : 0
  load_balancer_arn = aws_lb.api_nlb[0].arn
  port              = 6443
  protocol          = "TCP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.k3s_api[0].arn
  }
}

resource "aws_lb_target_group_attachment" "k3s_api_nodes" {
  count            = local.create_api_nlb_runtime ? local.effective_node_count : 0
  target_group_arn = aws_lb_target_group.k3s_api[0].arn
  target_id        = module.k3s_servers[count.index].instance_id
  port             = 6443
}
