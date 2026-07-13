package resource

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

// BuildDeployment deterministically renders the Phase 6.1 Deployment intent.
// It does not set owner references or contact the API server.
func BuildDeployment(service *platformv1alpha1.CoffeeShopService) (*appsv1.Deployment, error) {
	if err := Validate(service); err != nil {
		return nil, err
	}
	image, _ := imageReference(service.Spec.Image)
	replicas := service.Spec.Replicas
	labels := objectLabels(service)
	selectors := selectorLabels(service)

	container := corev1.Container{
		Name:            service.Name,
		Image:           image,
		ImagePullPolicy: service.Spec.Image.PullPolicy,
		Ports:           buildContainerPorts(service.Spec.Ports),
		Env:             buildEnv(service.Spec.Env),
		Resources:       *service.Spec.Resources.DeepCopy(),
	}
	if service.Spec.Probes != nil {
		container.StartupProbe = buildProbe(service.Spec.Probes.Startup)
		container.ReadinessProbe = buildProbe(service.Spec.Probes.Readiness)
		container.LivenessProbe = buildProbe(service.Spec.Probes.Liveness)
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: service.Name, Namespace: service.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selectors},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: objectLabels(service)},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{container}},
			},
		},
	}, nil
}

func buildContainerPorts(specs []platformv1alpha1.ContainerPortSpec) []corev1.ContainerPort {
	ports := make([]corev1.ContainerPort, 0, len(specs))
	for _, spec := range specs {
		ports = append(ports, corev1.ContainerPort{Name: spec.Name, ContainerPort: spec.ContainerPort, Protocol: spec.Protocol})
	}
	return ports
}

func buildEnv(specs []platformv1alpha1.EnvVarSpec) []corev1.EnvVar {
	env := make([]corev1.EnvVar, 0, len(specs))
	for _, spec := range specs {
		item := corev1.EnvVar{Name: spec.Name}
		if spec.Value != nil {
			item.Value = *spec.Value
		}
		if spec.ValueFrom != nil {
			item.ValueFrom = &corev1.EnvVarSource{}
			if spec.ValueFrom.ConfigMapKeyRef != nil {
				item.ValueFrom.ConfigMapKeyRef = &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: spec.ValueFrom.ConfigMapKeyRef.Name},
					Key:                  spec.ValueFrom.ConfigMapKeyRef.Key,
				}
			}
			if spec.ValueFrom.SecretKeyRef != nil {
				item.ValueFrom.SecretKeyRef = &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: spec.ValueFrom.SecretKeyRef.Name},
					Key:                  spec.ValueFrom.SecretKeyRef.Key,
				}
			}
		}
		env = append(env, item)
	}
	return env
}

func buildProbe(spec *platformv1alpha1.ProbeSpec) *corev1.Probe {
	if spec == nil {
		return nil
	}
	probe := &corev1.Probe{
		InitialDelaySeconds: spec.InitialDelaySeconds,
		TimeoutSeconds:      spec.TimeoutSeconds,
		PeriodSeconds:       spec.PeriodSeconds,
		SuccessThreshold:    spec.SuccessThreshold,
		FailureThreshold:    spec.FailureThreshold,
	}
	switch {
	case spec.HTTPGet != nil:
		probe.HTTPGet = &corev1.HTTPGetAction{
			Path:   spec.HTTPGet.Path,
			Port:   intstr.FromString(spec.HTTPGet.Port),
			Scheme: spec.HTTPGet.Scheme,
		}
	case spec.TCPSocket != nil:
		probe.TCPSocket = &corev1.TCPSocketAction{Port: intstr.FromString(spec.TCPSocket.Port)}
	case spec.GRPC != nil:
		grpc := &corev1.GRPCAction{Port: spec.GRPC.Port}
		if spec.GRPC.Service != "" {
			serviceName := spec.GRPC.Service
			grpc.Service = &serviceName
		}
		probe.GRPC = grpc
	}
	return probe
}
