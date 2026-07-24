# Generates private ECR repositories for all 6 microservices.
resource "aws_ecr_repository" "services" {
  for_each             = toset(["product", "counter", "barista", "kitchen", "proxy", "web"])
  name                 = "go-coffeeshop-${each.key}"
  image_tag_mutability = "IMMUTABLE"
  force_delete         = true
  image_scanning_configuration {
    scan_on_push = true
  }
  encryption_configuration {
    encryption_type = "AES256"
  }
  tags = {
    Environment = var.environment
    Project     = var.project_name
  }
}

# Generates dedicated private ECR repository for PlatformOwnershipGuard operator with IMMUTABLE tags.
resource "aws_ecr_repository" "platform_ownership_guard" {
  name                 = "platform-ownership-guard"
  image_tag_mutability = "IMMUTABLE"
  force_delete         = true
  image_scanning_configuration {
    scan_on_push = true
  }
  encryption_configuration {
    encryption_type = "AES256"
  }
  tags = {
    Environment = var.environment
    Project     = var.project_name
  }
}

# Retain a bounded candidate history. Twenty candidates leaves room for parallel
# feature/release work without expiring the digest currently under formal QA.
resource "aws_ecr_lifecycle_policy" "cleanup" {
  for_each   = aws_ecr_repository.services
  repository = each.value.name
  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Retain the last 20 release candidates"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 20
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}

resource "aws_ecr_lifecycle_policy" "cleanup_guard" {
  repository = aws_ecr_repository.platform_ownership_guard.name
  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Retain only the last 5 images to control storage costs"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 5
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}
