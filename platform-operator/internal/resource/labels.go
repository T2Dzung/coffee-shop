package resource

import platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"

const (
	// ManagedByValue is the stable Kubernetes recommended-label value.
	ManagedByValue = "coffeeshop-operator"
	// PartOfValue groups all CoffeeShop workloads.
	PartOfValue = "go-coffeeshop"
	// ServiceLabelKey is the immutable workload identity used by selectors.
	ServiceLabelKey = "platform.t2dzung.github.io/service"
)

func selectorLabels(service *platformv1alpha1.CoffeeShopService) map[string]string {
	return map[string]string{
		"app":           service.Name,
		ServiceLabelKey: service.Name,
	}
}

func objectLabels(service *platformv1alpha1.CoffeeShopService) map[string]string {
	return map[string]string{
		"app":                          service.Name,
		"app.kubernetes.io/name":       service.Name,
		"app.kubernetes.io/part-of":    PartOfValue,
		"app.kubernetes.io/managed-by": ManagedByValue,
		ServiceLabelKey:                service.Name,
	}
}
