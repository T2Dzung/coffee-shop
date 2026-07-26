package terraform.teardown

import rego.v1

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

test_deny_unknown if {
	deny["teardown delete is outside the ephemeral allowlist: aws_s3_bucket.unknown"] with input as {
		"resource_changes": [
			{"address": "aws_s3_bucket.unknown", "change": {"actions": ["delete"]}},
		],
	}
}
