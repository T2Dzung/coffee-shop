package terraform.reconcile

import rego.v1

test_allow_create_and_update if {
	result := deny with input as {
		"resource_changes": [
			{"address": "module.eks", "change": {"actions": ["update"]}},
			{"address": "aws_x.y", "change": {"actions": ["create"]}},
		],
	}
	count(result) == 0
}

test_deny_delete if {
	deny["reconcile rejects delete or replacement action for aws_db_instance.postgres"] with input as {
		"resource_changes": [
			{"address": "aws_db_instance.postgres", "change": {"actions": ["delete"]}},
		],
	}
}
