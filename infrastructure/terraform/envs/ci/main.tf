provider "aws" {
  region = var.aws_region
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

check "ci_account_boundary" {
  assert {
    condition     = data.aws_caller_identity.current.account_id == var.expected_aws_account_id
    error_message = "Refusing CI operation: caller does not match expected_aws_account_id."
  }
}

check "ci_region_boundary" {
  assert {
    condition     = data.aws_region.current.name == var.aws_region
    error_message = "Refusing CI operation: provider Region does not match aws_region."
  }
}

check "ci_environment_boundary" {
  assert {
    condition     = var.environment == "ci"
    error_message = "The dedicated runner root must use Environment=ci."
  }
}

check "ci_oidc_account_boundary" {
  assert {
    condition     = startswith(var.github_oidc_provider_arn, "arn:aws:iam::${var.expected_aws_account_id}:")
    error_message = "GitHub OIDC provider must belong to the selected CI account."
  }
}

check "ci_availability_zone_boundary" {
  assert {
    condition     = startswith(var.availability_zone, var.aws_region)
    error_message = "CI availability_zone must belong to aws_region."
  }
}

locals {
  cluster_name = coalesce(var.cluster_name, "${var.project_name}-ci-k3s")
  common_tags = merge({
    Project     = var.project_name
    Environment = "ci"
    ManagedBy   = "Terraform"
    Owner       = "PlatformEngineering"
    Phase       = "PROD-4"
    Lifecycle   = "ci-only"
  }, var.additional_tags)
}
