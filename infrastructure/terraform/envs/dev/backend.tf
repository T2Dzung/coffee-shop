# Partial backend configuration is supplied by platformctl from the selected DEV
# account/tfvars. State remains isolated at dev/terraform.tfstate.
terraform {
  backend "s3" {}
}
