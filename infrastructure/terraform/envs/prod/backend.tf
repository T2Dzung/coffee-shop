# Remote state stored in S3 with native S3 locking (Terraform v1.10+)
# Partial backend configuration: Bucket, key, and region are provided dynamically via backend-config
terraform {
  backend "s3" {}
}
