package terraform.dev_reconcile

deny contains message if {
	some change in input.resource_changes
	change.change.actions == ["delete"]
	retained_type(change.type)
	message := sprintf("DEV reconcile refuses deletion of retained resource %s", [change.address])
}

deny contains message if {
	some change in input.resource_changes
	change.change.actions == ["delete", "create"]
	retained_type(change.type)
	not safe_lifecycle_policy_replacement(change)
	message := sprintf("DEV reconcile refuses replacement of retained resource %s", [change.address])
}

# The AWS provider models a lifecycle-policy document update as replacement even
# though the ECR repository and its images are untouched. Permit only that narrow
# control-plane replacement when it remains attached to the exact same repository.
safe_lifecycle_policy_replacement(change) if {
	change.type == "aws_ecr_lifecycle_policy"
	change.change.before.repository == change.change.after.repository
}

retained_type(resource_type) if {
	resource_type in {
		"aws_s3_bucket", "aws_s3_bucket_versioning",
		"aws_s3_bucket_server_side_encryption_configuration",
		"aws_s3_bucket_public_access_block", "aws_ecr_repository",
		"aws_ecr_lifecycle_policy", "aws_iam_user", "aws_iam_access_key",
	}
}
