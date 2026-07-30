package terraform.teardown

import rego.v1

test_allow_empty_idempotent_teardown if {
	count(deny) == 0 with input as {"resource_changes": []}
}

test_allow_ephemeral if {
	result := deny with input as {
		"resource_changes": [
			{"address": "module.eks_nodes.aws_eks_node_group.this", "change": {"actions": ["delete"]}},
		],
	}
	count(result) == 0
}

test_deny_retained if {
	deny["teardown attempts to delete retained resource aws_ecr_repository.app[\"web\"]"] with input as {
		"resource_changes": [
			{"address": "aws_ecr_repository.app[\"web\"]", "change": {"actions": ["delete"]}},
		],
	}
}

test_allow_bounded_slo_runtime_cleanup if {
	result := deny with input as {
		"resource_changes": [
			{"address": "aws_synthetics_canary.golden_journey[0]", "change": {"actions": ["delete"]}},
			{"address": "aws_cloudwatch_dashboard.golden_journey[0]", "change": {"actions": ["delete"]}},
			{"address": "aws_iam_role_policy.synthetics_canary[0]", "change": {"actions": ["delete"]}},
			{"address": "aws_s3_object.golden_journey_code[0]", "change": {"actions": ["delete"]}},
		],
	}
	count(result) == 0
}

test_deny_similarly_named_slo_code_object if {
	deny["teardown delete is outside the ephemeral allowlist: aws_s3_object.golden_journey_code_backup"] with input as {
		"resource_changes": [
			{"address": "aws_s3_object.golden_journey_code_backup", "change": {"actions": ["delete"]}},
		],
	}
}

test_deny_retained_github_delivery_role if {
	deny["teardown attempts to delete retained resource aws_iam_role.github_delivery_role"] with input as {
		"resource_changes": [
			{"address": "aws_iam_role.github_delivery_role", "change": {"actions": ["delete"]}},
		],
	}
}

test_deny_unknown if {
	deny["teardown delete is outside the ephemeral allowlist: aws_s3_bucket.unknown"] with input as {
		"resource_changes": [
			{"address": "aws_s3_bucket.unknown", "change": {"actions": ["delete"]}},
		],
	}
}
