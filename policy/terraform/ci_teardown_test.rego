package terraform.ci_teardown

import rego.v1

test_allow_ci_destroy if {
	result := deny with input as {"resource_changes": [{
		"address": "aws_vpc.ci", "change": {
			"actions": ["delete"], "before": {"tags": {"Environment": "ci"}},
		},
	}]}
	count(result) == 0
}

test_deny_prod_destroy if {
	deny["CI teardown delete is outside the CI allowlist: module.eks_cluster.aws_eks_cluster.this"] with input as {
		"resource_changes": [{
			"address": "module.eks_cluster.aws_eks_cluster.this",
			"change": {"actions": ["delete"], "before": {"tags": {"Environment": "prod"}}},
		}]
	}
}
