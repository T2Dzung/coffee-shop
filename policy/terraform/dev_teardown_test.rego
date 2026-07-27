package terraform.dev_teardown

test_allow_empty_idempotent_teardown if {
	count(deny) == 0 with input as {"resource_changes": []}
}

test_allow_dev_runtime_delete if {
	count(deny) == 0 with input as {"resource_changes": [{
		"address": "module.k3s_servers[0].aws_instance.this", "type": "aws_instance",
		"change": {"actions": ["delete"], "before": {"tags": {"Environment": "dev", "ManagedBy": "Terraform"}}},
	}]}
}

test_allow_exact_legacy_haproxy_identities if {
	count(deny) == 0 with input as {"resource_changes": [
		{
			"address": "aws_iam_role.haproxy_role[0]",
			"type": "aws_iam_role",
			"change": {"actions": ["delete"], "before": {"name": "coffeeshop-dev-haproxy-role"}},
		},
		{
			"address": "aws_iam_instance_profile.haproxy_profile[0]",
			"type": "aws_iam_instance_profile",
			"change": {"actions": ["delete"], "before": {"name": "coffeeshop-dev-haproxy-profile"}},
		},
	]}
}

test_deny_unrelated_untagged_iam_role if {
	deny["DEV teardown resource lacks the DEV runtime boundary: aws_iam_role.haproxy_role[0]"] with input as {
		"resource_changes": [{
			"address": "aws_iam_role.haproxy_role[0]",
			"type": "aws_iam_role",
			"change": {"actions": ["delete"], "before": {"name": "unrelated-role"}},
		}],
	}
}

test_deny_retained_delete if {
	deny["DEV teardown attempts to delete retained resource aws_s3_bucket.postgres_backup"] with input as {
		"resource_changes": [{
			"address": "aws_s3_bucket.postgres_backup", "type": "aws_s3_bucket",
			"change": {"actions": ["delete"], "before": {"tags": {"Environment": "dev", "ManagedBy": "Terraform"}}},
		}],
	}
}

test_deny_update if {
	deny["DEV teardown allows delete-only mutations: aws_instance.node has [\"update\"]"] with input as {
		"resource_changes": [{
			"address": "aws_instance.node", "type": "aws_instance",
			"change": {"actions": ["update"], "before": {"tags": {"Environment": "dev", "ManagedBy": "Terraform"}}},
		}],
	}
}
