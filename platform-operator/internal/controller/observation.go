package controller

import (
	"context"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/resource"
)

// Observation holds the observed state of child resources.
type Observation struct {
	Deployment        *appsv1.Deployment
	Service           *corev1.Service
	DeploymentExists  bool
	ServiceExists     bool
	DeploymentDrifted bool
	ServiceDrifted    bool
}

// ObserveLiveState fetches live child resources and computes drift against desired state.
func ObserveLiveState(ctx context.Context, c client.Reader, service *platformv1alpha1.CoffeeShopService) (*Observation, error) {
	obs := &Observation{}

	// 1. Render desired state as the comparison baseline
	desiredDeploy, err := resource.BuildDeployment(service)
	if err != nil {
		return nil, err
	}

	desiredService, err := resource.BuildService(service)
	if err != nil {
		return nil, err
	}

	// 2. Get live Deployment
	liveDeploy := &appsv1.Deployment{}
	err = c.Get(ctx, client.ObjectKey{Namespace: service.Namespace, Name: service.Name}, liveDeploy)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
		obs.DeploymentExists = false
	} else {
		obs.DeploymentExists = true
		obs.Deployment = liveDeploy
		obs.DeploymentDrifted = checkDeploymentDrift(desiredDeploy, liveDeploy, service.Name)
	}

	// 3. Get live Service
	if desiredService != nil {
		liveService := &corev1.Service{}
		err = c.Get(ctx, client.ObjectKey{Namespace: service.Namespace, Name: service.Name}, liveService)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, err
			}
			obs.ServiceExists = false
		} else {
			obs.ServiceExists = true
			obs.Service = liveService
			obs.ServiceDrifted = checkServiceDrift(desiredService, liveService)
		}
	} else {
		// Service disabled in Spec
		// Check if live Service exists to report cleanup (drift)
		liveService := &corev1.Service{}
		err = c.Get(ctx, client.ObjectKey{Namespace: service.Namespace, Name: service.Name}, liveService)
		if err == nil {
			obs.ServiceExists = true
			obs.Service = liveService
			// If service enabled = false but live service still exists, it is considered drifted/needs pruning
			obs.ServiceDrifted = true
		} else if !apierrors.IsNotFound(err) {
			return nil, err
		}
	}

	return obs, nil
}

func checkDeploymentDrift(desired, live *appsv1.Deployment, containerName string) bool {
	// Compare replicas
	if desired.Spec.Replicas != nil && live.Spec.Replicas != nil {
		if *desired.Spec.Replicas != *live.Spec.Replicas {
			return true
		}
	} else if (desired.Spec.Replicas == nil) != (live.Spec.Replicas == nil) {
		return true
	}

	// Compare selectors
	if !reflect.DeepEqual(desired.Spec.Selector, live.Spec.Selector) {
		return true
	}

	// Find main container in live Deployment
	var liveMainContainer *corev1.Container
	for i := range live.Spec.Template.Spec.Containers {
		if live.Spec.Template.Spec.Containers[i].Name == containerName {
			liveMainContainer = &live.Spec.Template.Spec.Containers[i]
			break
		}
	}
	if liveMainContainer == nil {
		return true
	}

	desiredMainContainer := &desired.Spec.Template.Spec.Containers[0]

	// Compare container image
	if desiredMainContainer.Image != liveMainContainer.Image {
		return true
	}

	// Compare PullPolicy
	if desiredMainContainer.ImagePullPolicy != liveMainContainer.ImagePullPolicy {
		return true
	}

	// Compare Ports
	if len(desiredMainContainer.Ports) != len(liveMainContainer.Ports) {
		return true
	}
	for _, dp := range desiredMainContainer.Ports {
		found := false
		for _, lp := range liveMainContainer.Ports {
			if dp.ContainerPort == lp.ContainerPort && dp.Protocol == lp.Protocol && dp.Name == lp.Name {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}

	// Compare Env
	if len(desiredMainContainer.Env) != len(liveMainContainer.Env) {
		return true
	}
	for _, de := range desiredMainContainer.Env {
		found := false
		for _, le := range liveMainContainer.Env {
			if de.Name == le.Name {
				if de.Value == le.Value && reflect.DeepEqual(de.ValueFrom, le.ValueFrom) {
					found = true
				}
				break
			}
		}
		if !found {
			return true
		}
	}

	// Compare Resources (Requests & Limits for CPU/Memory)
	if !compareResourceList(desiredMainContainer.Resources.Requests, liveMainContainer.Resources.Requests) {
		return true
	}
	if !compareResourceList(desiredMainContainer.Resources.Limits, liveMainContainer.Resources.Limits) {
		return true
	}

	// Compare Probes
	if isProbeDrifted(desiredMainContainer.StartupProbe, liveMainContainer.StartupProbe) {
		return true
	}
	if isProbeDrifted(desiredMainContainer.ReadinessProbe, liveMainContainer.ReadinessProbe) {
		return true
	}
	if isProbeDrifted(desiredMainContainer.LivenessProbe, liveMainContainer.LivenessProbe) {
		return true
	}

	return false
}

func compareResourceList(desired, live corev1.ResourceList) bool {
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		dq, dFound := desired[name]
		lq, lFound := live[name]
		if dFound != lFound {
			return false
		}
		if dFound {
			if dq.Cmp(lq) != 0 {
				return false
			}
		}
	}
	return true
}

func isProbeDrifted(desired, live *corev1.Probe) bool {
	if desired == nil && live == nil {
		return false
	}
	if (desired == nil) != (live == nil) {
		return true
	}

	// Compare handler type
	if (desired.HTTPGet == nil) != (live.HTTPGet == nil) {
		return true
	}
	if desired.HTTPGet != nil {
		if desired.HTTPGet.Path != live.HTTPGet.Path ||
			desired.HTTPGet.Port.String() != live.HTTPGet.Port.String() ||
			desired.HTTPGet.Scheme != live.HTTPGet.Scheme {
			return true
		}
	}

	if (desired.TCPSocket == nil) != (live.TCPSocket == nil) {
		return true
	}
	if desired.TCPSocket != nil {
		if desired.TCPSocket.Port.String() != live.TCPSocket.Port.String() {
			return true
		}
	}

	if (desired.GRPC == nil) != (live.GRPC == nil) {
		return true
	}
	if desired.GRPC != nil {
		if desired.GRPC.Port != live.GRPC.Port {
			return true
		}
		dService := ""
		if desired.GRPC.Service != nil {
			dService = *desired.GRPC.Service
		}
		lService := ""
		if live.GRPC.Service != nil {
			lService = *live.GRPC.Service
		}
		if dService != lService {
			return true
		}
	}

	// Compare thresholds and delays (only compare if desired spec is greater than 0)
	if desired.InitialDelaySeconds != 0 && desired.InitialDelaySeconds != live.InitialDelaySeconds {
		return true
	}
	if desired.TimeoutSeconds != 0 && desired.TimeoutSeconds != live.TimeoutSeconds {
		return true
	}
	if desired.PeriodSeconds != 0 && desired.PeriodSeconds != live.PeriodSeconds {
		return true
	}
	if desired.SuccessThreshold != 0 && desired.SuccessThreshold != live.SuccessThreshold {
		return true
	}
	if desired.FailureThreshold != 0 && desired.FailureThreshold != live.FailureThreshold {
		return true
	}

	return false
}

func checkServiceDrift(desired, live *corev1.Service) bool {
	// Compare Type
	if desired.Spec.Type != live.Spec.Type {
		return true
	}

	// Compare selectors
	if !reflect.DeepEqual(desired.Spec.Selector, live.Spec.Selector) {
		return true
	}

	// Compare ports
	if len(desired.Spec.Ports) != len(live.Spec.Ports) {
		return true
	}
	for _, dp := range desired.Spec.Ports {
		found := false
		for _, lp := range live.Spec.Ports {
			if dp.Port == lp.Port && dp.TargetPort.String() == lp.TargetPort.String() && dp.Protocol == lp.Protocol && dp.Name == lp.Name {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}

	return false
}
