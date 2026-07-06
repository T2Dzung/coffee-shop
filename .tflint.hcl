plugin "aws" {
    enabled = true
    version = "0.33.0"
    source  = "github.com/terraform-linters/tflint-ruleset-aws"
}

config {
    format = "compact"
    call_module_type = "all"
    force = false
}

# Disable strict engine version checks in dev environment to minimize environment bootstrap overhead
rule "terraform_required_version" {
    enabled = false
}

# Disable provider version enforcement to avoid local provider lock drifts during iterative development
rule "terraform_required_providers" {
    enabled = false
}
