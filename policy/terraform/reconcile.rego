package terraform.reconcile

import rego.v1

deny contains message if {
	some change in input.resource_changes
	"delete" in change.change.actions
	not is_bounded_slo_cleanup(change.address)
	message := sprintf("reconcile rejects delete or replacement action for %s", [change.address])
}

bounded_slo_resources := {
	"aws_cloudwatch_dashboard.golden_journey",
	"aws_cloudwatch_metric_alarm.golden_journey",
	"aws_iam_role.synthetics_canary",
	"aws_iam_role_policy.synthetics_canary",
	"aws_synthetics_canary.golden_journey",
}

is_bounded_slo_cleanup(address) if {
	address in bounded_slo_resources
}

is_bounded_slo_cleanup(address) if {
	some resource in bounded_slo_resources
	startswith(address, sprintf("%s[", [resource]))
}
