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

test_allow_bounded_slo_cleanup if {
	result := deny with input as {
		"resource_changes": [
			{"address": "aws_synthetics_canary.golden_journey[0]", "change": {"actions": ["delete"]}},
			{"address": "aws_cloudwatch_metric_alarm.golden_journey[0]", "change": {"actions": ["delete"]}},
			{"address": "aws_cloudwatch_dashboard.golden_journey[0]", "change": {"actions": ["delete"]}},
			{"address": "aws_iam_role_policy.synthetics_canary[0]", "change": {"actions": ["delete"]}},
			{"address": "aws_iam_role.synthetics_canary[0]", "change": {"actions": ["delete"]}},
			{"address": "aws_s3_object.golden_journey_code[0]", "change": {"actions": ["delete"]}},
		],
	}
	count(result) == 0
}

test_deny_similarly_named_delete_outside_exact_o2_owner if {
	deny["reconcile rejects delete or replacement action for aws_synthetics_canary.golden_journey_other"] with input as {
		"resource_changes": [
			{"address": "aws_synthetics_canary.golden_journey_other", "change": {"actions": ["delete"]}},
		],
	}
}

test_deny_similarly_named_s3_object_delete_outside_exact_o2_owner if {
	deny["reconcile rejects delete or replacement action for aws_s3_object.golden_journey_code_backup"] with input as {
		"resource_changes": [
			{"address": "aws_s3_object.golden_journey_code_backup", "change": {"actions": ["delete"]}},
		],
	}
}
