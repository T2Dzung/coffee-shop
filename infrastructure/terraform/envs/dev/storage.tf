# ==============================================================================
# Dedicated EBS Volumes for Longhorn Distributed Storage (Phase 4)
# ==============================================================================

resource "aws_ebs_volume" "longhorn" {
  count             = var.longhorn_data_volume_enabled ? var.node_count : 0
  availability_zone = aws_subnet.public[count.index % length(var.public_subnet_cidrs)].availability_zone
  size              = var.longhorn_data_volume_size
  type              = var.longhorn_data_volume_type
  iops              = var.longhorn_data_volume_type == "gp3" ? var.longhorn_data_volume_iops : null
  throughput        = var.longhorn_data_volume_type == "gp3" ? var.longhorn_data_volume_throughput : null
  encrypted         = true

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-longhorn-data-${count.index + 1}"
  })
}

resource "aws_volume_attachment" "longhorn" {
  count       = var.longhorn_data_volume_enabled ? var.node_count : 0
  device_name = var.longhorn_data_device_name
  volume_id   = aws_ebs_volume.longhorn[count.index].id
  instance_id = module.k3s_servers[count.index].instance_id
}
