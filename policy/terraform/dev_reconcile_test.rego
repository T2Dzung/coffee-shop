package terraform.dev_reconcile

test_allow_runtime_create if {
	count(deny) == 0 with input as {"resource_changes": [{
		"address": "module.k3s_servers[0].aws_instance.this", "type": "aws_instance",
		"change": {"actions": ["create"]},
	}]}
}

test_deny_retained_delete if {
	deny["DEV reconcile refuses deletion of retained resource aws_s3_bucket.postgres_backup"] with input as {
		"resource_changes": [{
			"address": "aws_s3_bucket.postgres_backup", "type": "aws_s3_bucket",
			"change": {"actions": ["delete"]},
		}],
	}
}

test_allow_lifecycle_policy_replacement_for_same_repository if {
	count(deny) == 0 with input as {"resource_changes": [{
		"address": "aws_ecr_lifecycle_policy.cleanup[\"web\"]",
		"type": "aws_ecr_lifecycle_policy",
		"change": {
			"actions": ["delete", "create"],
			"before": {"repository": "go-coffeeshop-web", "policy": "old"},
			"after": {"repository": "go-coffeeshop-web", "policy": "new"},
		},
	}]}
}

test_deny_lifecycle_policy_replacement_for_different_repository if {
	deny["DEV reconcile refuses replacement of retained resource aws_ecr_lifecycle_policy.cleanup[\"web\"]"] with input as {
		"resource_changes": [{
			"address": "aws_ecr_lifecycle_policy.cleanup[\"web\"]",
			"type": "aws_ecr_lifecycle_policy",
			"change": {
				"actions": ["delete", "create"],
				"before": {"repository": "go-coffeeshop-web"},
				"after": {"repository": "some-other-repository"},
			},
		}],
	}
}
