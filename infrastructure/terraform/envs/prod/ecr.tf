locals {
  component_catalog = yamldecode(file("${path.root}/../../../../platform/components.yaml"))
  ecr_repositories = {
    for component in local.component_catalog.components : component.name => component.imageRepository
  }
  candidate_ecr_repositories = {
    for component in local.component_catalog.components :
    component.name => "coffeeshop-candidate-${component.imageRepository}"
  }
}

# The component catalog key was normalized from an underscore to the public
# hyphenated component name. Preserve the existing repository and lifecycle
# policy in state instead of planning a delete/create of the same AWS object.
moved {
  from = aws_ecr_repository.app["platform_ownership_guard"]
  to   = aws_ecr_repository.app["platform-ownership-guard"]
}

moved {
  from = aws_ecr_lifecycle_policy.app["platform_ownership_guard"]
  to   = aws_ecr_lifecycle_policy.app["platform-ownership-guard"]
}

resource "aws_ecr_repository" "app" {
  for_each             = local.ecr_repositories
  name                 = each.value
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = local.common_tags
}

resource "aws_ecr_lifecycle_policy" "app" {
  for_each   = local.ecr_repositories
  repository = aws_ecr_repository.app[each.key].name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 10 images to save storage cost"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}

# Candidate repositories are an account-level artifact boundary, not runner cache.
# They remain after CI/PROD compute teardown so an approved digest can be promoted
# without rebuilding it.
resource "aws_ecr_repository" "candidate" {
  for_each             = local.candidate_ecr_repositories
  name                 = each.value
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.common_tags, {
    Environment = "ci"
    Lifecycle   = "retained-artifact"
  })
}

resource "aws_ecr_lifecycle_policy" "candidate" {
  for_each   = local.candidate_ecr_repositories
  repository = aws_ecr_repository.candidate[each.key].name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the latest 10 immutable release candidates"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 10
      }
      action = { type = "expire" }
    }]
  })
}
