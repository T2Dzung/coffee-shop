variable "github_owner" {
  description = "GitHub user or organization that owns the repository."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+$", var.github_owner))
    error_message = "github_owner must be a GitHub user or organization name."
  }
}

variable "repository_name" {
  description = "Existing repository whose governance settings are managed."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+$", var.repository_name))
    error_message = "repository_name must be a repository name without owner prefix."
  }
}

variable "repository_visibility" {
  description = "Expected repository visibility."
  type        = string
  default     = "public"

  validation {
    condition     = contains(["public", "private", "internal"], var.repository_visibility)
    error_message = "repository_visibility must be public, private, or internal."
  }
}

variable "import_existing_repository" {
  description = "Import an existing repository into this governance state."
  type        = bool
  default     = true
}

variable "import_existing_environments" {
  description = "Import delivery environments that already exist; false creates missing environments."
  type        = bool
  default     = true
}

variable "import_existing_actions_variables" {
  description = "Import repository Actions variables that already exist; false creates them."
  type        = bool
  default     = true
}

variable "repository_actions_variables" {
  description = "Non-secret repository Actions variables used by delivery workflows."
  type        = map(string)
  default     = {}

  validation {
    condition = alltrue([
      for name, value in var.repository_actions_variables :
      can(regex("^[A-Z][A-Z0-9_]*$", name)) && trimspace(value) != ""
    ])
    error_message = "Actions variable names must be uppercase identifiers and values must be non-empty."
  }
}

variable "ruleset_enforcement" {
  description = "Enforcement mode for the default-branch ruleset."
  type        = string
  default     = "active"

  validation {
    condition     = contains(["active", "disabled"], var.ruleset_enforcement)
    error_message = "ruleset_enforcement must be active or disabled for a personal repository."
  }
}

variable "ruleset_bypass_username" {
  description = "Optional repository owner allowed to bypass the default-branch ruleset for lab recovery."
  type        = string
  default     = null

  validation {
    condition = (
      var.ruleset_bypass_username == null ||
      can(regex("^[A-Za-z0-9-]+$", var.ruleset_bypass_username))
    )
    error_message = "ruleset_bypass_username must be null or one GitHub username."
  }
}

variable "required_status_checks" {
  description = "Stable public job names required before default-branch merge."
  type        = set(string)
  default     = ["CI Status", "DevSecOps Status"]

  validation {
    condition     = length(var.required_status_checks) > 0
    error_message = "At least one required status check is required."
  }
}

variable "required_approving_review_count" {
  description = "Required PR approvals. Keep 0 for a single-user lab; use 1 or more with independent reviewers."
  type        = number
  default     = 0

  validation {
    condition = (
      var.required_approving_review_count >= 0 &&
      var.required_approving_review_count <= 6 &&
      floor(var.required_approving_review_count) == var.required_approving_review_count
    )
    error_message = "required_approving_review_count must be an integer from 0 through 6."
  }
}

variable "require_code_owner_review" {
  description = "Require CODEOWNERS approval for matching changes."
  type        = bool
  default     = false
}

variable "require_last_push_approval" {
  description = "Require approval from someone other than the actor who made the latest push."
  type        = bool
  default     = false
}

variable "delivery_environments" {
  description = "GitHub Environments used by the current delivery workflows."
  type        = set(string)
  default     = ["ci-build", "dev", "qa", "prod", "prod-emergency"]
}

variable "environment_reviewer_usernames" {
  description = "Optional reviewer usernames per Environment. Omit for the single-user lab."
  type        = map(list(string))
  default     = {}

  validation {
    condition = alltrue([
      for environment in keys(var.environment_reviewer_usernames) :
      contains(var.delivery_environments, environment)
    ])
    error_message = "Environment reviewer keys must exist in delivery_environments."
  }
}

variable "prevent_self_review_environments" {
  description = "Environments where the workflow initiator cannot approve their own deployment."
  type        = set(string)
  default     = []

  validation {
    condition = alltrue([
      for environment in var.prevent_self_review_environments :
      contains(var.delivery_environments, environment)
    ])
    error_message = "prevent_self_review_environments must be a subset of delivery_environments."
  }
}
