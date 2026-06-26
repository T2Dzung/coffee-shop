provider "aws" {
  region = var.aws_region
}

# Fetch the active AWS Account ID and identity details dynamically
data "aws_caller_identity" "current" {}

locals {
  # Use custom name if provided, otherwise fallback to default naming convention
  bucket_name = var.custom_bucket_name != null ? var.custom_bucket_name : "${var.project_name}-terraform-state-${data.aws_caller_identity.current.account_id}"
  
  # Common tags merged with user-defined additional tags
  common_tags = merge(
    {
      Project   = var.project_name
      ManagedBy = "Terraform"
      Role      = "IaC State Storage"
    },
    var.additional_tags
  )
}

data "aws_region" "current" {}

# Resource group to consolidate all backend resources (S3 and KMS) in one dashboard
resource "aws_resourcegroups_group" "bootstrap_group" {
  name        = "${var.project_name}-backend-resources"
  description = "Groups S3 Bucket and KMS Key used for Terraform Remote State"

  resource_query {
    query = jsonencode({
      ResourceTypeFilters = [
        "AWS::S3::Bucket",
        "AWS::KMS::Key"
      ]
      TagFilters = [
        {
          Key    = "Project"
          Values = [var.project_name]
        }
      ]
    })
  }

  tags = local.common_tags
}

# ==============================================================================
# OUTPUTS
# ==============================================================================

output "config" {
  description = "Consolidated configuration details for the S3 remote backend to be used in backend.tf"
  value = {
    bucket       = aws_s3_bucket.terraform_state.bucket
    region       = data.aws_region.current.name
    encrypt      = true
    use_lockfile = true
  }
}
