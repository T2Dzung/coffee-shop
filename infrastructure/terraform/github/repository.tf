# This portfolio repository is intentionally public. Runtime credentials and
# environment secrets remain in GitHub encrypted stores or local mode-0600 files.
#trivy:ignore:GIT-0001
resource "github_repository" "this" {
  name       = var.repository_name
  visibility = var.repository_visibility

  allow_auto_merge       = true
  allow_merge_commit     = false
  allow_rebase_merge     = false
  allow_squash_merge     = true
  delete_branch_on_merge = true

  lifecycle {
    prevent_destroy = true
  }
}

# This project adopts an existing repository. Set import_existing_repository=false
# only when intentionally creating a new repository from this root.
import {
  for_each = var.import_existing_repository ? toset(["existing"]) : toset([])
  to       = github_repository.this
  id       = var.repository_name
}
