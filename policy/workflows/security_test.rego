package workflows.security

import rego.v1

test_allow_explicit_hosted_workflow if {
	result := deny with input as {
		"uses_secrets_inherit": false,
		"unpinned_actions": [],
		"pull_request_self_hosted": false,
	}
	count(result) == 0
}

test_deny_inherit if {
	deny["workflow uses secrets: inherit instead of an explicit secret contract"] with input as {
		"uses_secrets_inherit": true,
		"unpinned_actions": [],
		"pull_request_self_hosted": false,
	}
}

test_deny_unknown_action_ref if {
	deny["action reference is not pinned to a full commit SHA: actions/checkout@main"] with input as {
		"uses_secrets_inherit": false,
		"unpinned_actions": ["actions/checkout@main"],
		"pull_request_self_hosted": false,
	}
}

test_deny_untrusted_self_hosted if {
	deny["pull_request code can reach a self-hosted runner"] with input as {
		"uses_secrets_inherit": false,
		"unpinned_actions": [],
		"pull_request_self_hosted": true,
	}
}

test_deny_candidate_build_without_preflight if {
	deny["candidate ARC build is not gated by hosted ECR preflight"] with input as {
		"candidate_build_without_ecr_preflight": true,
	}
}

test_deny_candidate_preflight_on_self_hosted if {
	deny["candidate ECR preflight must run on a GitHub-hosted runner"] with input as {
		"candidate_preflight_not_hosted": true,
	}
}

test_deny_non_atomic_prod_release_set if {
	deny["standard PROD release set must fan in to one protected desired-state PR"] with input as {
		"prod_standard_missing_atomic_fan_in": true,
	}
}

test_deny_ungated_prod_status_job if {
	deny["PROD status job must ignore non-default push branches"] with input as {
		"prod_status_missing_default_branch_gate": true,
	}
}

test_deny_candidate_build_without_toolchain_contract if {
	deny["candidate ARC build does not enforce its typed toolchain profile"] with input as {
		"candidate_build_missing_toolchain": true,
	}
}

test_deny_aws_cli_in_arc_build_action if {
	deny["ARC build action calls AWS CLI; cloud API checks belong to the hosted preflight"] with input as {
		"arc_build_uses_aws_cli": true,
	}
}
