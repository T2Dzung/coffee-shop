package resource

import (
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

var requiredResources = []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}

// Validate performs defensive checks that either cannot be expressed safely in
// the current CEL schema (quantity ordering) or protect direct builder callers.
func Validate(service *platformv1alpha1.CoffeeShopService) error {
	if service == nil {
		return errors.New("CoffeeShopService must not be nil")
	}
	if _, err := imageReference(service.Spec.Image); err != nil {
		return err
	}

	for _, name := range requiredResources {
		request, requestFound := service.Spec.Resources.Requests[name]
		limit, limitFound := service.Spec.Resources.Limits[name]
		if !requestFound || !limitFound {
			return fmt.Errorf("resource %q requires both request and limit", name)
		}
		if request.Sign() <= 0 || limit.Sign() <= 0 {
			return fmt.Errorf("resource %q request and limit must be positive", name)
		}
		if limit.Cmp(request) < 0 {
			return fmt.Errorf("resource %q limit must be greater than or equal to request", name)
		}
	}

	if service.Spec.Service != nil && service.Spec.Service.Enabled {
		declaredPorts := make(map[string]struct{}, len(service.Spec.Ports))
		for _, port := range service.Spec.Ports {
			declaredPorts[port.Name] = struct{}{}
		}
		for _, port := range service.Spec.Service.Ports {
			if _, found := declaredPorts[port.TargetPort]; !found {
				return fmt.Errorf("service targetPort %q does not reference a container port", port.TargetPort)
			}
		}
	}
	return nil
}

func imageReference(image platformv1alpha1.ImageSpec) (string, error) {
	if (image.Tag == "") == (image.Digest == "") {
		return "", errors.New("exactly one image tag or digest is required")
	}
	if image.Digest != "" {
		return fmt.Sprintf("%s@%s", image.Repository, image.Digest), nil
	}
	return fmt.Sprintf("%s:%s", image.Repository, image.Tag), nil
}
