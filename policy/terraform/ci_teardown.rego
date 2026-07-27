package terraform.ci_teardown

import rego.v1

allowed_prefixes := [
	"aws_iam_instance_profile.host",
	"aws_iam_role.host",
	"aws_iam_role.build",
	"aws_iam_role_policy.host",
	"aws_iam_role_policy.build",
	"aws_internet_gateway.ci",
	"aws_route_table.ci",
	"aws_route_table_association.ci",
	"aws_security_group.runner",
	"aws_subnet.ci",
	"aws_vpc.ci",
	"aws_vpc_security_group_egress_rule.outbound",
	"aws_vpc_security_group_ingress_rule.ssh",
	"module.runner_host.",
]

deletes contains address if {
	some change in input.resource_changes
	"delete" in change.change.actions
	address := change.address
}

deny contains message if {
	some address in deletes
	not is_allowed(address)
	message := sprintf("CI teardown delete is outside the CI allowlist: %s", [address])
}

deny contains message if {
	some change in input.resource_changes
	"delete" in change.change.actions
	before := change.change.before
	before != null
	tags := object.get(before, "tags", {})
	count(tags) > 0
	object.get(tags, "Environment", "") != "ci"
	message := sprintf("CI teardown refuses non-CI tagged resource: %s", [change.address])
}

is_allowed(address) if {
	some prefix in allowed_prefixes
	startswith(address, prefix)
}
