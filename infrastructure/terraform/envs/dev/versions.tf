# Pin the dev control-plane toolchain so rebuilds do not silently change APIs.
terraform {
  required_version = "= 1.15.7"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 5.100.0"
    }
  }
}
