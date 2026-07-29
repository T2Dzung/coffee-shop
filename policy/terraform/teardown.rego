package terraform.teardown

import rego.v1

retained_prefixes := [
	"aws_ecr_repository.app",
	"aws_ecr_lifecycle_policy.app",
	"aws_ecr_repository.candidate",
	"aws_ecr_lifecycle_policy.candidate",
]

allowed_prefixes := [
	"aws_db_instance.",
	"aws_db_subnet_group.",
	"aws_security_group.rds",
	"aws_secretsmanager_secret.",
	"aws_cloudwatch_log_group.",
	"aws_cloudwatch_metric_alarm.",
	"aws_cloudwatch_dashboard.golden_journey",
	"aws_iam_role_policy.synthetics_canary",
	"aws_synthetics_canary.golden_journey",
	"aws_eks_addon.",
	"aws_eks_pod_identity_association.",
	"aws_iam_role_policy_attachment.",
	"aws_iam_policy.",
	"aws_iam_role.",
	"module.eks_nodes.",
	"module.eks_cluster.",
	"module.vpc.",
]

deletes contains address if {
	some change in input.resource_changes
	"delete" in change.change.actions
	address := change.address
}

deny contains message if {
	some address in deletes
	is_retained(address)
	message := sprintf("teardown attempts to delete retained resource %s", [address])
}

deny contains message if {
	some address in deletes
	not is_retained(address)
	not is_allowed(address)
	message := sprintf("teardown delete is outside the ephemeral allowlist: %s", [address])
}

is_retained(address) if {
	address == "aws_budgets_budget.prod_budget"
}

is_retained(address) if {
	address == "aws_iam_openid_connect_provider.github"
}

is_retained(address) if {
	address in {
		"aws_iam_role_policy_attachment.github_delivery_attach",
		"aws_iam_role_policy_attachment.github_emergency_delivery_attach",
		"aws_iam_policy.github_delivery_policy",
		"aws_iam_role.github_delivery_role",
		"aws_iam_role.github_emergency_delivery_role",
	}
}

is_retained(address) if {
	some prefix in retained_prefixes
	startswith(address, prefix)
}

is_allowed(address) if {
	some prefix in allowed_prefixes
	startswith(address, prefix)
}
