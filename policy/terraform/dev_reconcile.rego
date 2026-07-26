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
	message := sprintf("DEV reconcile refuses replacement of retained resource %s", [change.address])
}

retained_type(resource_type) if {
	resource_type in {
		"aws_s3_bucket", "aws_s3_bucket_versioning",
		"aws_s3_bucket_server_side_encryption_configuration",
		"aws_s3_bucket_public_access_block", "aws_ecr_repository",
		"aws_ecr_lifecycle_policy", "aws_iam_user", "aws_iam_access_key",
	}
}
