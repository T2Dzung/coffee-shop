package terraform.reconcile

import rego.v1

deny contains message if {
	some change in input.resource_changes
	"delete" in change.change.actions
	message := sprintf("reconcile rejects delete or replacement action for %s", [change.address])
}
