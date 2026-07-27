resource "github_repository_environment" "delivery" {
  for_each = var.delivery_environments

  repository  = github_repository.this.name
  environment = each.value

  lifecycle {
    prevent_destroy = true
  }

  can_admins_bypass   = false
  prevent_self_review = contains(var.prevent_self_review_environments, each.value)

  dynamic "reviewers" {
    for_each = length(lookup(var.environment_reviewer_usernames, each.value, [])) > 0 ? [true] : []
    content {
      users = [
        for username in var.environment_reviewer_usernames[each.value] :
        data.github_user.environment_reviewer[username].id
      ]
    }
  }

  deployment_branch_policy {
    protected_branches     = true
    custom_branch_policies = false
  }
}

import {
  for_each = var.import_existing_environments ? var.delivery_environments : toset([])
  to       = github_repository_environment.delivery[each.value]
  id       = "${var.repository_name}:${each.value}"
}
