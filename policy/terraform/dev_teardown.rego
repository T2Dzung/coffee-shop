package terraform.dev_teardown

mutations := [change |
	some change in input.resource_changes
	not change.change.actions in {["no-op"], ["read"]}
]

deletes := [change |
	some change in mutations
	change.change.actions == ["delete"]
]

deny contains message if {
	some change in mutations
	change.change.actions != ["delete"]
	message := sprintf("DEV teardown allows delete-only mutations: %s has %v", [change.address, change.change.actions])
}

deny contains message if {
	some change in deletes
	retained_type(change.type)
	message := sprintf("DEV teardown attempts to delete retained resource %s", [change.address])
}

deny contains message if {
	some change in deletes
	not tagged_dev(change)
	not untagged_runtime_child_type(change.type)
	message := sprintf("DEV teardown resource lacks the DEV runtime boundary: %s", [change.address])
}

tagged_dev(change) if {
	change.change.before.tags.Environment == "dev"
	change.change.before.tags.ManagedBy == "Terraform"
}

untagged_runtime_child_type(resource_type) if {
	resource_type in {
		"aws_volume_attachment", "aws_eip_association", "aws_lb_listener",
		"aws_lb_target_group_attachment", "aws_vpc_security_group_ingress_rule",
		"aws_vpc_security_group_egress_rule", "aws_iam_role_policy_attachment",
	}
}

retained_type(resource_type) if {
	resource_type in {
		"aws_s3_bucket", "aws_s3_bucket_versioning",
		"aws_s3_bucket_server_side_encryption_configuration",
		"aws_s3_bucket_public_access_block", "aws_ecr_repository",
		"aws_ecr_lifecycle_policy", "aws_iam_user", "aws_iam_access_key",
		"aws_vpc", "aws_subnet", "aws_route_table", "aws_route_table_association",
		"aws_internet_gateway",
	}
}
