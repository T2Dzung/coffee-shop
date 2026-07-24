provider "aws" {
  region = var.aws_region
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

check "expected_aws_account" {
  assert {
    condition     = data.aws_caller_identity.current.account_id == var.expected_aws_account_id
    error_message = "Refusing PROD operation: active AWS account does not match expected_aws_account_id."
  }
}

check "expected_aws_region" {
  assert {
    condition     = data.aws_region.current.name == var.aws_region
    error_message = "Refusing PROD operation: provider Region does not match aws_region."
  }
}

locals {
  # Tag contract: Project, Environment, ManagedBy, Owner, Phase
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      ManagedBy   = "Terraform"
      Owner       = "PlatformEngineering"
      Phase       = "PROD-1"
    },
    var.additional_tags
  )
}
