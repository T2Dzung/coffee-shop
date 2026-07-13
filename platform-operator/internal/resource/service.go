package resource

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

// BuildService deterministically renders a ClusterIP Service. A nil result means
// the API explicitly disabled Service creation.
func BuildService(service *platformv1alpha1.CoffeeShopService) (*corev1.Service, error) {
	if err := Validate(service); err != nil {
		return nil, err
	}
	if service.Spec.Service == nil || !service.Spec.Service.Enabled {
		return nil, nil
	}

	ports := make([]corev1.ServicePort, 0, len(service.Spec.Service.Ports))
	for _, spec := range service.Spec.Service.Ports {
		ports = append(ports, corev1.ServicePort{
			Name: spec.Name, Port: spec.Port, TargetPort: intstr.FromString(spec.TargetPort), Protocol: spec.Protocol,
		})
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: service.Name, Namespace: service.Namespace, Labels: objectLabels(service)},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: selectorLabels(service), Ports: ports},
	}, nil
}
