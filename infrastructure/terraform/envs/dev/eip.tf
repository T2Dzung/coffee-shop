# Elastic IP for Dev Kubernetes HA Cluster Primary Node
resource "aws_eip" "primary_eip" {
  instance = module.dev_k8s_primary.instance_id
  domain   = "vpc"

  tags = {
    Name        = "${var.project_name}-${var.environment}-primary-eip"
    Environment = var.environment
  }
}
