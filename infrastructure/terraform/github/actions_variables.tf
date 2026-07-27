resource "github_actions_variable" "repository" {
  for_each = var.repository_actions_variables

  repository    = github_repository.this.name
  variable_name = each.key
  value         = each.value

  lifecycle {
    prevent_destroy = true
  }
}

import {
  for_each = var.import_existing_actions_variables ? var.repository_actions_variables : {}
  to       = github_actions_variable.repository[each.key]
  id       = "${var.repository_name}:${each.key}"
}
