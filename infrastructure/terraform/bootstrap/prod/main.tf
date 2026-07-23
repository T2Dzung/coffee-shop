provider "aws" {
  region = var.aws_region
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

check "expected_aws_account" {
  assert {
    condition     = data.aws_caller_identity.current.account_id == var.expected_aws_account_id
    error_message = "Refusing PROD bootstrap operation: caller does not match expected_aws_account_id."
  }
}

check "expected_aws_region" {
  assert {
    condition     = data.aws_region.current.name == var.aws_region
    error_message = "Refusing PROD bootstrap operation: provider Region does not match aws_region."
  }
}

module "backend" {
  source = "../../modules/terraform-backend"

  project_name       = var.project_name
  environment        = "prod"
  custom_bucket_name = var.custom_bucket_name
  allowed_principals = var.allowed_principals
  additional_tags    = var.additional_tags
}
