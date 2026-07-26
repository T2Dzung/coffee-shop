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
