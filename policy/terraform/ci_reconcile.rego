package terraform.ci_reconcile

import rego.v1

deny contains message if {
	some change in input.resource_changes
	"delete" in change.change.actions
	not allowed_operator_ssh_cidr_rotation(change)
	message := sprintf("CI reconcile rejects delete or replacement action for %s", [change.address])
}

# operator_ssh_cidrs is a for_each set. Rotating a reviewed /32 therefore appears
# as one delete and one create rather than an in-place update. Permit deletion only
# for the canonical TCP/22 rule; the create side is still checked by the ingress
# policy below and Terraform's CIDR validation.
allowed_operator_ssh_cidr_rotation(change) if {
	change.type == "aws_vpc_security_group_ingress_rule"
	startswith(change.address, "aws_vpc_security_group_ingress_rule.ssh[\"")
	change.change.actions == ["delete"]
	change.change.after == null
	change.change.before.security_group_id != ""
	change.change.before.description == "SSH from reviewed operator CIDR"
	change.change.before.ip_protocol == "tcp"
	change.change.before.from_port == 22
	change.change.before.to_port == 22
	change.change.before.cidr_ipv4 != "0.0.0.0/0"
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
