package workflows.security

import rego.v1

deny contains "workflow uses secrets: inherit instead of an explicit secret contract" if {
	input.uses_secrets_inherit
}

deny contains message if {
	some reference in input.unpinned_actions
	message := sprintf("action reference is not pinned to a full commit SHA: %s", [reference])
}

deny contains "pull_request code can reach a self-hosted runner" if {
	input.pull_request_self_hosted
}

deny contains "candidate ARC build is not gated by hosted ECR preflight" if {
	input.candidate_build_without_ecr_preflight
}

deny contains "candidate ECR preflight must run on a GitHub-hosted runner" if {
	input.candidate_preflight_not_hosted
}

deny contains "candidate ARC build does not enforce its typed toolchain profile" if {
	input.candidate_build_missing_toolchain
}

deny contains "ARC build action calls AWS CLI; cloud API checks belong to the hosted preflight" if {
	input.arc_build_uses_aws_cli
}

deny contains "standard PROD release set must fan in to one protected desired-state PR" if {
	input.prod_standard_missing_atomic_fan_in
}
