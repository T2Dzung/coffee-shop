package kubernetes.prod

import rego.v1

test_allow_pinned_bounded_workload if {
	result := deny with input as {
		"kind": "Deployment",
		"metadata": {"name": "web", "namespace": "coffeeshop"},
		"spec": {"template": {"spec": {"containers": [{
			"name": "web",
			"image": "example/web@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"resources": {"requests": {"cpu": "100m", "memory": "128Mi"}},
		}]}}},
	}
	count(result) == 0
}

test_deny_mutable_image if {
	deny["Deployment/web container web is not pinned by digest"] with input as {
		"kind": "Deployment",
		"metadata": {"name": "web", "namespace": "coffeeshop"},
		"spec": {"template": {"spec": {"containers": [{
			"name": "web",
			"image": "example/web:latest",
			"resources": {"requests": {"cpu": "100m", "memory": "128Mi"}},
		}]}}},
	}
}

test_ignore_non_prod_namespace if {
	result := deny with input as {
		"kind": "Deployment",
		"metadata": {"name": "tool", "namespace": "dev-tools"},
		"spec": {"template": {"spec": {"containers": [{"name": "tool", "image": "tool:latest"}]}}},
	}
	count(result) == 0
}
