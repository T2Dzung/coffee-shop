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
