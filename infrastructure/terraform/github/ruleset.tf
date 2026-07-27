resource "github_repository_ruleset" "default_branch" {
  name        = "protected-default-branch"
  repository  = github_repository.this.name
  target      = "branch"
  enforcement = var.ruleset_enforcement

  lifecycle {
    prevent_destroy = true
  }

  dynamic "bypass_actors" {
    for_each = var.ruleset_bypass_username == null ? [] : [true]
    content {
      actor_id    = data.github_user.ruleset_bypass[0].id
      actor_type  = "User"
      bypass_mode = "always"
    }
  }

  conditions {
    ref_name {
      include = ["~DEFAULT_BRANCH"]
      exclude = []
    }
  }

  rules {
    deletion                = true
    non_fast_forward        = true
    required_linear_history = true

    pull_request {
      allowed_merge_methods             = ["squash"]
      dismiss_stale_reviews_on_push     = var.required_approving_review_count > 0
      require_code_owner_review         = var.require_code_owner_review
      require_last_push_approval        = var.require_last_push_approval
      required_approving_review_count   = var.required_approving_review_count
      required_review_thread_resolution = true
    }

    required_status_checks {
      strict_required_status_checks_policy = var.strict_required_status_checks_policy

      dynamic "required_check" {
        for_each = var.required_status_checks
        content {
          context = required_check.value
        }
      }
    }
  }
}
