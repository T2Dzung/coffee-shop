terraform {
  # Governance state is independent from DEV/CI/PROD infrastructure state.
  # Supply bucket, key, region and KMS settings through -backend-config.
  backend "s3" {}
}
