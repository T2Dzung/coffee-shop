package config.environment

import rego.v1

deny contains "bootstrap and foundation state keys must be distinct" if {
	input.bootstrap_state_key == input.foundation_state_key
}

deny contains "PROD state keys must remain under the prod prefix" if {
	not startswith(input.bootstrap_state_key, "prod/")
}

deny contains "PROD state keys must remain under the prod prefix" if {
	not startswith(input.foundation_state_key, "prod/")
}

deny contains "PROD teardown selector must not match CI" if {
	input.environment == "ci"
}
