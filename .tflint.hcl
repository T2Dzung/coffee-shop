plugin "aws" {
    enabled = true
    version = "0.33.0"
    source  = "github.com/terraform-linters/tflint-ruleset-aws"
}

config {
    format = "compact"
    module = true
    force = false
}
