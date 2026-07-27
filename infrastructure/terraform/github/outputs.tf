output "repository" {
  description = "Repository governed by this state."
  value       = github_repository.this.full_name
}

output "default_branch_ruleset_id" {
  description = "GitHub ruleset protecting the default branch."
  value       = github_repository_ruleset.default_branch.ruleset_id
}

output "delivery_environments" {
  description = "Environment names governed by this state."
  value       = sort(keys(github_repository_environment.delivery))
}

output "repository_actions_variables" {
  description = "Non-secret Actions variable names governed by this state."
  value       = sort(keys(github_actions_variable.repository))
}
