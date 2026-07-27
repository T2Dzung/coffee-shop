provider "github" {
  owner = var.github_owner
}

locals {
  environment_reviewer_usernames = toset(flatten([
    for usernames in values(var.environment_reviewer_usernames) : usernames
  ]))
}

data "github_user" "environment_reviewer" {
  for_each = local.environment_reviewer_usernames
  username = each.value
}

data "github_user" "ruleset_bypass" {
  count    = var.ruleset_bypass_username == null ? 0 : 1
  username = var.ruleset_bypass_username
}
