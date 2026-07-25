module "eks_cluster" {
  source = "../../modules/eks-cluster"

  cluster_name                 = var.cluster_name
  cluster_version              = var.cluster_version
  subnet_ids                   = module.vpc.private_subnet_ids
  enabled_cluster_log_types    = ["api", "audit", "authenticator"]
  log_retention_in_days        = 7
  endpoint_public_access_cidrs = var.cluster_endpoint_public_access_cidrs
  tags                         = local.common_tags
}

module "eks_nodes" {
  source = "../../modules/eks-managed-node"

  cluster_name      = module.eks_cluster.cluster_name
  node_group_name   = "system-nodes"
  subnet_ids        = module.vpc.private_subnet_ids
  instance_types    = var.node_instance_types
  capacity_type     = "ON_DEMAND"
  desired_size      = var.node_desired_size
  min_size          = 1
  max_size          = 3
  disk_size         = var.node_disk_size
  volume_type       = "gp3"
  volume_iops       = var.node_disk_iops
  volume_throughput = var.node_disk_throughput
  tags              = local.common_tags
}

resource "aws_eks_addon" "ebs_csi" {
  cluster_name  = module.eks_cluster.cluster_name
  addon_name    = "aws-ebs-csi-driver"
  addon_version = var.ebs_csi_addon_version

  tags = local.common_tags

  depends_on = [aws_eks_pod_identity_association.ebs_csi]
}

resource "aws_eks_addon" "cloudwatch_observability" {
  cluster_name  = module.eks_cluster.cluster_name
  addon_name    = "amazon-cloudwatch-observability"
  addon_version = var.cloudwatch_observability_addon_version
  configuration_values = jsonencode({
    manager = {
      applicationSignals = {
        autoMonitor = {
          monitorAllServices = false
        }
      }
    }
  })

  tags = local.common_tags

  depends_on = [aws_eks_pod_identity_association.cloudwatch_agent]
}
