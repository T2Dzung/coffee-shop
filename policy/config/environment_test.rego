package config.environment

import rego.v1

test_allow_prod_boundaries if {
	result := deny with input as {
		"environment": "prod",
		"bootstrap_state_key": "prod/bootstrap.tfstate",
		"foundation_state_key": "prod/foundation.tfstate",
	}
	count(result) == 0
}

test_deny_shared_state if {
	deny["bootstrap and foundation state keys must be distinct"] with input as {
		"environment": "prod",
		"bootstrap_state_key": "prod/shared.tfstate",
		"foundation_state_key": "prod/shared.tfstate",
	}
}

test_deny_cross_environment_prefix if {
	deny["PROD state keys must remain under the prod prefix"] with input as {
		"environment": "prod",
		"bootstrap_state_key": "dev/bootstrap.tfstate",
		"foundation_state_key": "prod/foundation.tfstate",
	}
}
