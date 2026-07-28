package terraform.ci_reconcile

import rego.v1

test_allow_ci_create if {
	result := deny with input as {"resource_changes": [{
		"address": "aws_vpc.ci", "mode": "managed", "type": "aws_vpc",
		"change": {"actions": ["create"], "after": {"tags": {"Environment": "ci"}}},
	}]}
	count(result) == 0
}

test_deny_prod_address if {
	deny["CI plan contains PROD-coupled resource address: aws_iam_role.prod"] with input as {
		"resource_changes": [{
			"address": "aws_iam_role.prod", "mode": "managed", "type": "aws_iam_role",
			"change": {"actions": ["create"], "after": {"tags": {"Environment": "ci"}}},
		}]
	}
}

test_deny_wrong_tag if {
	deny["CI managed resource lacks Environment=ci: aws_vpc.ci"] with input as {
		"resource_changes": [{
			"address": "aws_vpc.ci", "mode": "managed", "type": "aws_vpc",
			"change": {"actions": ["create"], "after": {"tags": {"Environment": "prod"}}},
		}]
	}
}

test_deny_open_ingress if {
	deny["CI security group exposes ingress to the Internet: aws_vpc_security_group_ingress_rule.ssh"] with input as {
		"resource_changes": [{
			"address": "aws_vpc_security_group_ingress_rule.ssh", "mode": "managed",
			"type": "aws_vpc_security_group_ingress_rule",
			"change": {"actions": ["create"], "after": {"cidr_ipv4": "0.0.0.0/0"}},
		}]
	}
}

test_allow_reviewed_operator_ssh_cidr_rotation if {
	result := deny with input as {
		"resource_changes": [
			{
				"address": "aws_vpc_security_group_ingress_rule.ssh[\"42.112.49.72/32\"]",
				"mode": "managed",
				"type": "aws_vpc_security_group_ingress_rule",
				"change": {
					"actions": ["delete"],
					"before": {
						"security_group_id": "sg-ci-runner",
						"description": "SSH from reviewed operator CIDR",
						"ip_protocol": "tcp",
						"from_port": 22,
						"to_port": 22,
						"cidr_ipv4": "42.112.49.72/32",
					},
					"after": null,
				},
			},
			{
				"address": "aws_vpc_security_group_ingress_rule.ssh[\"58.187.66.5/32\"]",
				"mode": "managed",
				"type": "aws_vpc_security_group_ingress_rule",
				"change": {
					"actions": ["create"],
					"before": null,
					"after": {
						"security_group_id": "sg-ci-runner",
						"description": "SSH from reviewed operator CIDR",
						"ip_protocol": "tcp",
						"from_port": 22,
						"to_port": 22,
						"cidr_ipv4": "58.187.66.5/32",
					},
				},
			},
		],
	}
	count(result) == 0
}

test_deny_non_ssh_rule_disguised_as_cidr_rotation if {
	deny["CI reconcile rejects delete or replacement action for aws_vpc_security_group_ingress_rule.ssh[\"42.112.49.72/32\"]"] with input as {
		"resource_changes": [{
			"address": "aws_vpc_security_group_ingress_rule.ssh[\"42.112.49.72/32\"]",
			"mode": "managed",
			"type": "aws_vpc_security_group_ingress_rule",
			"change": {
				"actions": ["delete"],
				"before": {
					"security_group_id": "sg-ci-runner",
					"description": "SSH from reviewed operator CIDR",
					"ip_protocol": "tcp",
					"from_port": 443,
					"to_port": 443,
					"cidr_ipv4": "42.112.49.72/32",
				},
				"after": null,
			},
		}]
	}
}

test_deny_prod_iam_permission if {
	deny["CI IAM policy contains PROD delivery permission eks:UpdateClusterConfig in aws_iam_role_policy.build"] with input as {
		"resource_changes": [{
			"address": "aws_iam_role_policy.build", "mode": "managed", "type": "aws_iam_role_policy",
			"change": {"actions": ["create"], "after": {
				"policy": "{\"Statement\":[{\"Action\":[\"eks:UpdateClusterConfig\"],\"Resource\":\"*\"}]}"
			}},
		}]
	}
}

test_allow_candidate_preflight_permission if {
	result := deny with input as {
		"resource_changes": [{
			"address": "aws_iam_role_policy.build", "mode": "managed", "type": "aws_iam_role_policy",
			"change": {"actions": ["update"], "after": {
				"policy": "{\"Statement\":[{\"Action\":[\"ecr:DescribeRepositories\",\"ecr:PutImage\"],\"Resource\":\"candidate\"}]}"
			}},
		}]
	}
	count(result) == 0
}

test_deny_missing_candidate_preflight_permission if {
	deny["CI candidate build policy is missing ecr:DescribeRepositories required by hosted preflight"] with input as {
		"resource_changes": [{
			"address": "aws_iam_role_policy.build", "mode": "managed", "type": "aws_iam_role_policy",
			"change": {"actions": ["update"], "after": {
				"policy": "{\"Statement\":[{\"Action\":[\"ecr:PutImage\"],\"Resource\":\"candidate\"}]}"
			}},
		}]
	}
}
