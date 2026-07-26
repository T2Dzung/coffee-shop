package kubernetes.prod

import rego.v1

workload if {
	input.kind in {"Deployment", "StatefulSet", "Job", "CronJob"}
	input.metadata.namespace == "coffeeshop"
}

pod_spec := input.spec.template.spec if {
	input.kind in {"Deployment", "StatefulSet", "Job"}
}

pod_spec := input.spec.jobTemplate.spec.template.spec if {
	input.kind == "CronJob"
}

deny contains message if {
	workload
	some container in pod_spec.containers
	not contains(container.image, "@sha256:")
	message := sprintf("%s/%s container %s is not pinned by digest", [
		input.kind, input.metadata.name, container.name,
	])
}

deny contains message if {
	workload
	some container in pod_spec.containers
	not container.resources.requests.cpu
	message := sprintf("%s/%s container %s has no CPU request", [
		input.kind, input.metadata.name, container.name,
	])
}

deny contains message if {
	workload
	some container in pod_spec.containers
	not container.resources.requests.memory
	message := sprintf("%s/%s container %s has no memory request", [
		input.kind, input.metadata.name, container.name,
	])
}
