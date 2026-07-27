# ==============================================================================
# CloudWatch Log Groups for Container & Workload Telemetry
# ==============================================================================

resource "aws_cloudwatch_log_group" "application_logs" {
  name              = "/aws/containerinsights/${var.cluster_name}/application"
  retention_in_days = var.cw_log_retention_days

  tags = local.common_tags
}

resource "aws_cloudwatch_log_group" "host_logs" {
  name              = "/aws/containerinsights/${var.cluster_name}/host"
  retention_in_days = var.cw_log_retention_days

  tags = local.common_tags
}

resource "aws_cloudwatch_log_group" "dataplane_logs" {
  name              = "/aws/containerinsights/${var.cluster_name}/dataplane"
  retention_in_days = var.cw_log_retention_days

  tags = local.common_tags
}

resource "aws_cloudwatch_log_group" "performance_logs" {
  name              = "/aws/containerinsights/${var.cluster_name}/performance"
  retention_in_days = var.cw_log_retention_days

  tags = local.common_tags
}

# ==============================================================================
# Scoped CloudWatch Alarms (Core Operational Metrics)
# ==============================================================================

resource "aws_cloudwatch_metric_alarm" "rds_free_storage" {
  alarm_name          = "${var.project_name}-${var.environment}-alarm-rds-free-storage"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 1
  metric_name         = "FreeStorageSpace"
  namespace           = "AWS/RDS"
  period              = 300
  statistic           = "Average"
  threshold           = 2000000000 # 2GB
  alarm_description   = "Alarm when RDS PostgreSQL free storage space drops below 2GB"
  dimensions = {
    DBInstanceIdentifier = aws_db_instance.postgres.identifier
  }
  treat_missing_data = "missing"

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "node_cpu_high" {
  alarm_name          = "${var.project_name}-${var.environment}-alarm-node-cpu-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "node_cpu_utilization"
  namespace           = "ContainerInsights"
  period              = 300
  statistic           = "Average"
  threshold           = 85
  alarm_description   = "Alarm when average EKS worker node CPU utilization exceeds 85%"
  dimensions = {
    ClusterName = var.cluster_name
  }
  treat_missing_data = "missing"

  tags = local.common_tags
}
