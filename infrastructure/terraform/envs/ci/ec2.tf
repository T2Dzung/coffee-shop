module "runner_host" {
  source = "../../modules/ec2-instance"

  project_name                = var.project_name
  environment                 = "ci"
  instance_type               = var.instance_type
  ami_id                      = var.ami_id
  key_name                    = var.key_name
  subnet_id                   = aws_subnet.ci.id
  security_group_ids          = [aws_security_group.runner.id]
  iam_instance_profile        = aws_iam_instance_profile.host.name
  associate_public_ip_address = true
  disk_size                   = var.root_volume_size
  root_volume_iops            = var.root_volume_iops
  root_volume_throughput      = var.root_volume_throughput
  volume_type                 = "gp3"
  volume_encrypted            = true
  enable_spot                 = false

  user_data = <<-EOF
    #!/bin/bash
    set -euo pipefail
    apt-get update -y
    DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl jq python3 python3-apt
    systemctl enable --now amazon-ssm-agent || true
  EOF

  additional_tags = merge(local.common_tags, {
    Name         = local.cluster_name
    Cluster      = local.cluster_name
    K8sRole      = "ci-runner"
    K3sBootstrap = "true"
    RunnerPlane  = "trusted-build"
  })
}
