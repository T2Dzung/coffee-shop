# ==============================================================================
# AWS Secrets Manager Secret for CoffeeShop Application & Broker DSNs
# ==============================================================================

resource "aws_secretsmanager_secret" "coffeeshop_app_secret" {
  name                    = "/coffeeshop/${var.environment}/application"
  description             = "Application and message broker connection credentials for CoffeeShop PROD"
  recovery_window_in_days = 0

  tags = local.common_tags
}
