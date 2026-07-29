locals {
  slo_canary_name = "coffeeshop-prod-api"
  slo_runbook_url = "https://github.com/${var.github_repository}/blob/main/docs/runbooks/prod-golden-journey.md"
  slo_target_path = "/api/v1/api/item-types"
  slo_tags        = merge(local.common_tags, { Phase = "O2" })
}

data "archive_file" "golden_journey" {
  type        = "zip"
  output_path = "${path.module}/canary/coffeeshop-golden-journey.zip"

  source {
    content  = file("${path.module}/canary/index.js")
    filename = "index.js"
  }
}

data "aws_resourcegroupstaggingapi_resources" "prod_alb" {
  count = var.slo_runtime_enabled ? 1 : 0

  resource_type_filters = ["elasticloadbalancing:loadbalancer"]

  tag_filter {
    key    = "ingress.k8s.aws/stack"
    values = ["coffeeshop/coffeeshop-prod-alb-ingress"]
  }
}

data "aws_lb" "prod" {
  count = var.slo_runtime_enabled ? 1 : 0
  arn   = one(data.aws_resourcegroupstaggingapi_resources.prod_alb[0].resource_tag_mapping_list[*].resource_arn)
}

resource "aws_s3_bucket" "synthetics_artifacts" {
  bucket        = "${var.project_name}-${var.environment}-synthetics-${data.aws_caller_identity.current.account_id}-${var.aws_region}"
  force_destroy = false

  lifecycle {
    prevent_destroy = true
  }

  tags = local.slo_tags
}

resource "aws_s3_bucket_versioning" "synthetics_artifacts" {
  bucket = aws_s3_bucket.synthetics_artifacts.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "synthetics_artifacts" {
  bucket = aws_s3_bucket.synthetics_artifacts.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "synthetics_artifacts" {
  bucket = aws_s3_bucket.synthetics_artifacts.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "synthetics_artifacts" {
  bucket = aws_s3_bucket.synthetics_artifacts.id

  rule {
    id     = "expire-synthetics-artifacts"
    status = "Enabled"

    filter {
      prefix = "canary/"
    }

    expiration {
      days = var.slo_artifact_retention_days
    }

    noncurrent_version_expiration {
      noncurrent_days = var.slo_artifact_retention_days
    }
  }
}

resource "aws_s3_bucket_policy" "synthetics_artifacts" {
  bucket = aws_s3_bucket.synthetics_artifacts.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "DenyInsecureTransport"
      Effect    = "Deny"
      Principal = "*"
      Action    = "s3:*"
      Resource = [
        aws_s3_bucket.synthetics_artifacts.arn,
        "${aws_s3_bucket.synthetics_artifacts.arn}/*",
      ]
      Condition = {
        Bool = { "aws:SecureTransport" = "false" }
      }
    }]
  })
}

resource "aws_iam_role" "synthetics_canary" {
  count = var.slo_runtime_enabled ? 1 : 0
  name  = "${var.project_name}-${var.environment}-synthetics-canary"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = local.slo_tags
}

resource "aws_iam_role_policy" "synthetics_canary" {
  count = var.slo_runtime_enabled ? 1 : 0
  name  = "${var.project_name}-${var.environment}-synthetics-canary"
  role  = aws_iam_role.synthetics_canary[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "WriteCanaryArtifacts"
        Effect = "Allow"
        Action = ["s3:GetObject", "s3:PutObject"]
        Resource = [
          "${aws_s3_bucket.synthetics_artifacts.arn}/canary/*",
        ]
      },
      {
        Sid      = "ReadArtifactBucketLocation"
        Effect   = "Allow"
        Action   = ["s3:GetBucketLocation"]
        Resource = aws_s3_bucket.synthetics_artifacts.arn
      },
      {
        Sid      = "DiscoverArtifactBucket"
        Effect   = "Allow"
        Action   = ["s3:ListAllMyBuckets"]
        Resource = "*"
      },
      {
        Sid    = "WriteCanaryLogs"
        Effect = "Allow"
        Action = ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = [
          "arn:aws:logs:${var.aws_region}:${data.aws_caller_identity.current.account_id}:log-group:/aws/lambda/cwsyn-${local.slo_canary_name}-*",
          "arn:aws:logs:${var.aws_region}:${data.aws_caller_identity.current.account_id}:log-group:/aws/lambda/cwsyn-${local.slo_canary_name}-*:log-stream:*",
        ]
      },
      {
        Sid      = "PublishSyntheticsMetrics"
        Effect   = "Allow"
        Action   = ["cloudwatch:PutMetricData"]
        Resource = "*"
        Condition = {
          StringEquals = { "cloudwatch:namespace" = "CloudWatchSynthetics" }
        }
      },
    ]
  })
}

resource "aws_synthetics_canary" "golden_journey" {
  count = var.slo_runtime_enabled ? 1 : 0

  name                 = local.slo_canary_name
  artifact_s3_location = "s3://${aws_s3_bucket.synthetics_artifacts.id}/canary/"
  execution_role_arn   = aws_iam_role.synthetics_canary[0].arn
  handler              = "index.handler"
  runtime_version      = var.synthetics_runtime_version
  zip_file             = data.archive_file.golden_journey.output_path
  start_canary         = true

  delete_lambda            = true
  success_retention_period = var.slo_artifact_retention_days
  failure_retention_period = var.slo_artifact_retention_days

  schedule {
    expression = var.slo_canary_schedule_expression
  }

  run_config {
    active_tracing     = false
    timeout_in_seconds = 30
    environment_variables = {
      TARGET_URL     = "http://${data.aws_lb.prod[0].dns_name}${local.slo_target_path}"
      MIN_ITEM_TYPES = tostring(var.slo_minimum_item_types)
    }
  }

  depends_on = [
    aws_iam_role_policy.synthetics_canary,
    aws_s3_bucket_lifecycle_configuration.synthetics_artifacts,
    aws_s3_bucket_policy.synthetics_artifacts,
    aws_s3_bucket_public_access_block.synthetics_artifacts,
    aws_s3_bucket_server_side_encryption_configuration.synthetics_artifacts,
    aws_s3_bucket_versioning.synthetics_artifacts,
  ]

  tags = local.slo_tags
}

resource "aws_cloudwatch_metric_alarm" "golden_journey" {
  count = var.slo_runtime_enabled ? 1 : 0

  alarm_name          = "${var.project_name}-${var.environment}-alarm-golden-journey"
  alarm_description   = "Synthetic item-types journey failed or stopped reporting. Runbook: ${local.slo_runbook_url}"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 3
  datapoints_to_alarm = 2
  metric_name         = "SuccessPercent"
  namespace           = "CloudWatchSynthetics"
  period              = 300
  statistic           = "Average"
  threshold           = 100
  treat_missing_data  = "breaching"

  dimensions = {
    CanaryName = aws_synthetics_canary.golden_journey[0].name
  }

  tags = local.slo_tags
}

resource "aws_cloudwatch_dashboard" "golden_journey" {
  count          = var.slo_runtime_enabled ? 1 : 0
  dashboard_name = "${var.project_name}-${var.environment}-golden-journey"

  dashboard_body = jsonencode({
    start = "-PT24H"
    widgets = [
      {
        type   = "text"
        x      = 0
        y      = 0
        width  = 24
        height = 3
        properties = {
          markdown = "# CoffeeShop PROD synthetic availability\nSLI: successful valid `item-types` runs / all valid runs. Initial target: **>=99% over rolling 24h**. Expected samples: **288/day**. Missing data is shown separately and treated as breaching by the fast alarm. [Runbook](${local.slo_runbook_url})"
        }
      },
      {
        type   = "metric"
        x      = 0
        y      = 3
        width  = 24
        height = 3
        properties = {
          title                = "Rolling 24h SLI and observed-run denominator"
          view                 = "singleValue"
          region               = var.aws_region
          period               = 300
          setPeriodToTimeRange = true
          metrics = [
            ["CloudWatchSynthetics", "SuccessPercent", "CanaryName", aws_synthetics_canary.golden_journey[0].name, { stat = "Average", label = "Valid-run success (%)" }],
            ["CloudWatchSynthetics", "SuccessPercent", "CanaryName", aws_synthetics_canary.golden_journey[0].name, { stat = "SampleCount", label = "Observed valid runs (expected 288)" }],
          ]
        }
      },
      {
        type   = "metric"
        x      = 0
        y      = 6
        width  = 12
        height = 6
        properties = {
          title  = "Synthetic success percent"
          view   = "timeSeries"
          region = var.aws_region
          period = 300
          stat   = "Average"
          yAxis  = { left = { min = 0, max = 100 } }
          metrics = [[
            "CloudWatchSynthetics", "SuccessPercent", "CanaryName", aws_synthetics_canary.golden_journey[0].name,
          ]]
        }
      },
      {
        type   = "metric"
        x      = 12
        y      = 6
        width  = 12
        height = 6
        properties = {
          title  = "Run samples and duration"
          view   = "timeSeries"
          region = var.aws_region
          period = 300
          metrics = [
            ["CloudWatchSynthetics", "SuccessPercent", "CanaryName", aws_synthetics_canary.golden_journey[0].name, { stat = "SampleCount", label = "Observed runs" }],
            ["CloudWatchSynthetics", "Duration", "CanaryName", aws_synthetics_canary.golden_journey[0].name, { stat = "p95", yAxis = "right" }],
          ]
        }
      },
    ]
  })
}
