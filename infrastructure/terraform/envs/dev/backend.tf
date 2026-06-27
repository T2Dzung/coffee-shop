# Remote state stored in S3 with native S3 locking (Terraform v1.10+)
terraform {
  backend "s3" {
    bucket       = "coffeeshop-terraform-state-423623841278"
    key          = "dev/terraform.tfstate"
    region       = "ap-southeast-1"
    encrypt      = true
    use_lockfile = true
  }
}
