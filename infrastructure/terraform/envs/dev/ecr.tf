# Generates private ECR repositories for all 6 microservices.
resource "aws_ecr_repository" "services" {
  for_each             = toset(["product", "counter", "barista", "kitchen", "proxy", "web"])
  name                 = "go-coffeeshop-${each.key}"
  image_tag_mutability = "MUTABLE"
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

# Retains only the 5 most recent images to remain comfortably within the ECR Free Tier limits (500MB).
resource "aws_ecr_lifecycle_policy" "cleanup" {
  for_each   = aws_ecr_repository.services
  repository = each.value.name
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
