package terraform.ci_reconcile

import rego.v1

deny contains message if {
	some change in input.resource_changes
	"delete" in change.change.actions
	message := sprintf("CI reconcile rejects delete or replacement action for %s", [change.address])
}

deny contains message if {
	some change in input.resource_changes
	change.type == "aws_vpc_security_group_ingress_rule"
	change.change.after.cidr_ipv4 == "0.0.0.0/0"
	message := sprintf("CI security group exposes ingress to the Internet: %s", [change.address])
}

deny contains message if {
	some change in input.resource_changes
	change.type == "aws_iam_role_policy"
	policy_doc := json.unmarshal(change.change.after.policy)
	some statement in policy_doc.Statement
	some action in array.concat([], statement.Action)
	starts_with_prod_permission(action)
	message := sprintf("CI IAM policy contains PROD delivery permission %s in %s", [action, change.address])
}

starts_with_prod_permission(action) if { startswith(lower(action), "eks:") }
starts_with_prod_permission(action) if { startswith(lower(action), "rds:") }
starts_with_prod_permission(action) if { startswith(lower(action), "elasticloadbalancing:") }
starts_with_prod_permission(action) if { lower(action) == "iam:passrole" }

deny contains "CI candidate build policy is missing ecr:DescribeRepositories required by hosted preflight" if {
	some change in input.resource_changes
	change.address == "aws_iam_role_policy.build"
	change.change.after != null
	policy_doc := json.unmarshal(change.change.after.policy)
	not policy_has_action(policy_doc, "ecr:DescribeRepositories")
}

policy_has_action(policy_doc, required) if {
	some statement in policy_doc.Statement
	some action in array.concat([], statement.Action)
	lower(action) == lower(required)
}

deny contains message if {
	some change in input.resource_changes
	change.mode == "managed"
	change.type in {"aws_vpc", "aws_subnet", "aws_security_group", "aws_instance", "aws_iam_role"}
	change.change.after != null
	tags := object.get(change.change.after, "tags", {})
	object.get(tags, "Environment", "") != "ci"
	message := sprintf("CI managed resource lacks Environment=ci: %s", [change.address])
}

deny contains message if {
	some change in input.resource_changes
	contains(lower(change.address), "prod")
	message := sprintf("CI plan contains PROD-coupled resource address: %s", [change.address])
}
