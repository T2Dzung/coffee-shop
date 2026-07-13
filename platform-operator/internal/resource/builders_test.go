package resource

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

func builderFixture() *platformv1alpha1.CoffeeShopService {
	return &platformv1alpha1.CoffeeShopService{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "coffeeshop"},
		Spec: platformv1alpha1.CoffeeShopServiceSpec{
			ManagementPolicy: platformv1alpha1.ManagementPolicyObserve,
			AdoptionPolicy:   platformv1alpha1.AdoptionPolicyNever,
			Image: platformv1alpha1.ImageSpec{
				Repository: "registry.example/web", Tag: "v1.0.0", PullPolicy: corev1.PullIfNotPresent,
			},
			Replicas: 2,
			Ports:    []platformv1alpha1.ContainerPortSpec{{Name: "http", ContainerPort: 8888, Protocol: corev1.ProtocolTCP}},
			Service: &platformv1alpha1.ServiceSpec{
				Enabled: true,
				Ports:   []platformv1alpha1.ServicePortSpec{{Name: "http", Port: 8888, TargetPort: "http", Protocol: corev1.ProtocolTCP}},
			},
			Env: []platformv1alpha1.EnvVarSpec{
				{Name: "REVERSE_PROXY_URL", Value: new("/api")},
				{Name: "WEB_PORT", ValueFrom: &platformv1alpha1.EnvVarSourceSpec{
					ConfigMapKeyRef: &platformv1alpha1.ConfigMapKeySelectorSpec{Name: "coffeeshop-config", Key: "WEB_PORT"},
				}},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: apiresource.MustParse("50m"), corev1.ResourceMemory: apiresource.MustParse("64Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: apiresource.MustParse("250m"), corev1.ResourceMemory: apiresource.MustParse("128Mi"),
				},
			},
			Probes: &platformv1alpha1.ProbesSpec{
				Readiness: &platformv1alpha1.ProbeSpec{
					HTTPGet:        &platformv1alpha1.HTTPGetAction{Path: "/", Port: "http", Scheme: corev1.URISchemeHTTP},
					TimeoutSeconds: 1, PeriodSeconds: 5, SuccessThreshold: 1, FailureThreshold: 3,
				},
			},
		},
	}
}

func TestBuildersAreDeterministicAndDoNotMutateInput(t *testing.T) {
	service := builderFixture()
	original := service.DeepCopy()

	firstDeployment, err := BuildDeployment(service)
	if err != nil {
		t.Fatalf("BuildDeployment() error = %v", err)
	}
	secondDeployment, err := BuildDeployment(service)
	if err != nil {
		t.Fatalf("second BuildDeployment() error = %v", err)
	}
	firstService, err := BuildService(service)
	if err != nil {
		t.Fatalf("BuildService() error = %v", err)
	}
	secondService, err := BuildService(service)
	if err != nil {
		t.Fatalf("second BuildService() error = %v", err)
	}

	if !reflect.DeepEqual(firstDeployment, secondDeployment) {
		t.Fatal("BuildDeployment() returned different objects for the same input")
	}
	if !reflect.DeepEqual(firstService, secondService) {
		t.Fatal("BuildService() returned different objects for the same input")
	}
	if !reflect.DeepEqual(service, original) {
		t.Fatal("builders mutated the CoffeeShopService input")
	}

	*firstDeployment.Spec.Replicas = 99
	if service.Spec.Replicas != 2 {
		t.Fatal("builder output aliases CoffeeShopService replicas")
	}

	container := firstDeployment.Spec.Template.Spec.Containers[0]
	if container.Image != "registry.example/web:v1.0.0" {
		t.Fatalf("image = %q", container.Image)
	}
	if container.Env[1].ValueFrom.ConfigMapKeyRef.Name != "coffeeshop-config" {
		t.Fatal("ConfigMap reference was not rendered")
	}
	if container.ReadinessProbe == nil || container.ReadinessProbe.HTTPGet.Port.StrVal != "http" {
		t.Fatal("HTTP readiness probe was not rendered")
	}
	if firstService.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("service type = %q", firstService.Spec.Type)
	}
	if firstService.Spec.Ports[0].TargetPort.StrVal != "http" {
		t.Fatal("named targetPort was not preserved")
	}
}

func TestBuildServiceReturnsNilWhenDisabled(t *testing.T) {
	service := builderFixture()
	service.Spec.Service.Enabled = false
	built, err := BuildService(service)
	if err != nil {
		t.Fatalf("BuildService() error = %v", err)
	}
	if built != nil {
		t.Fatal("BuildService() should return nil when disabled")
	}
}

func TestValidateRejectsResourceLimitBelowRequest(t *testing.T) {
	service := builderFixture()
	service.Spec.Resources.Limits[corev1.ResourceMemory] = apiresource.MustParse("32Mi")
	if err := Validate(service); err == nil {
		t.Fatal("Validate() accepted a limit lower than the request")
	}
}

func TestBuildDeploymentUsesDigestWithoutLatestFallback(t *testing.T) {
	service := builderFixture()
	service.Spec.Image.Tag = ""
	service.Spec.Image.Digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	deployment, err := BuildDeployment(service)
	if err != nil {
		t.Fatalf("BuildDeployment() error = %v", err)
	}
	expected := "registry.example/web@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if deployment.Spec.Template.Spec.Containers[0].Image != expected {
		t.Fatalf("image = %q, want %q", deployment.Spec.Template.Spec.Containers[0].Image, expected)
	}
}
