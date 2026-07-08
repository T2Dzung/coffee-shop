# ==============================================================================
# K3s Server Instances (3-Server HA)
# ==============================================================================

module "k3s_servers" {
  source = "../../modules/ec2-instance"
  count  = var.node_count

  project_name  = var.project_name
  environment   = var.environment
  instance_type = var.k3s_instance_type
  key_name      = var.key_name
  ami_id        = local.resolved_ami_id

  # Assign to subnets round-robin
  subnet_id = aws_subnet.public[count.index % length(var.public_subnet_cidrs)].id

  security_group_ids   = [aws_security_group.k3s_node_sg.id]
  iam_instance_profile = aws_iam_instance_profile.dev_profile.name

  # Storage configuration
  disk_size              = var.k3s_root_volume_size
  root_volume_iops       = var.k3s_root_volume_iops
  root_volume_throughput = var.k3s_root_volume_throughput

  # Map public IP so Ansible can access the instances in Phase 1
  associate_public_ip_address = true

  enable_spot = false

  user_data = <<-EOF
              #!/bin/bash
              set -e
              echo "=== STARTING DEV NODE BOOTSTRAP ===" >> /var/log/user-data-done.log
              apt-get update -y
              apt-get install -y apt-transport-https ca-certificates curl gnupg lsb-release jq git unzip python3 python3-pip open-iscsi nfs-common
              echo "=== DEV NODE BOOTSTRAP COMPLETED: $(date) ===" >> /var/log/user-data-done.log
              EOF

  additional_tags = {
    Name         = "${var.cluster_name}-server-${count.index + 1}"
    K8sRole      = "server"
    K3sBootstrap = count.index == 0 ? "true" : "false"
    NodeIndex    = tostring(count.index + 1)
    Cluster      = var.cluster_name
  }
}
