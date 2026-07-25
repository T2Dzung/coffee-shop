locals {
  # Preserve the existing build artifact contract so PROD promotion can reuse the same
  # service names rather than introducing a second repository naming convention.
  ecr_repositories = {
    web                      = "go-coffeeshop-web"
    proxy                    = "go-coffeeshop-proxy"
    product                  = "go-coffeeshop-product"
    counter                  = "go-coffeeshop-counter"
    barista                  = "go-coffeeshop-barista"
    kitchen                  = "go-coffeeshop-kitchen"
    migrate                  = "go-coffeeshop-migrate"
    platform_ownership_guard = "platform-ownership-guard"
  }
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
