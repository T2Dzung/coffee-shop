package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

func TestParentGenerationPredicate(t *testing.T) {
	p := ParentGenerationPredicate()
	oldParent := &platformv1alpha1.CoffeeShopService{ObjectMeta: metav1.ObjectMeta{Generation: 3}}

	statusOnly := oldParent.DeepCopy()
	statusOnly.Status.ReadyReplicas = 1
	if p.Update(event.UpdateEvent{ObjectOld: oldParent, ObjectNew: statusOnly}) {
		t.Fatal("status-only parent update should not enqueue")
	}

	specChanged := oldParent.DeepCopy()
	specChanged.Generation = 4
	if !p.Update(event.UpdateEvent{ObjectOld: oldParent, ObjectNew: specChanged}) {
		t.Fatal("parent generation change should enqueue")
	}
}

func TestRelevantChildChangePredicateDeployment(t *testing.T) {
	p := RelevantChildChangePredicate()
	oldDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Generation:      1,
			Labels:          map[string]string{"app": "web"},
			OwnerReferences: []metav1.OwnerReference{{Name: "web"}},
		},
		Spec: appsv1.DeploymentSpec{Replicas: new(int32(2))},
	}

	annotationOnly := oldDeployment.DeepCopy()
	annotationOnly.Generation = 2 // Deployment annotations may increment generation.
	annotationOnly.Annotations = map[string]string{"example.com/note": "keep"}
	if p.Update(event.UpdateEvent{ObjectOld: oldDeployment, ObjectNew: annotationOnly}) {
		t.Fatal("irrelevant annotation-only Deployment update should not enqueue")
	}

	specChanged := oldDeployment.DeepCopy()
	specChanged.Spec.Replicas = new(int32(3))
	if !p.Update(event.UpdateEvent{ObjectOld: oldDeployment, ObjectNew: specChanged}) {
		t.Fatal("Deployment spec change should enqueue")
	}

	statusChanged := oldDeployment.DeepCopy()
	statusChanged.Status.AvailableReplicas = 2
	if !p.Update(event.UpdateEvent{ObjectOld: oldDeployment, ObjectNew: statusChanged}) {
		t.Fatal("Deployment availability change should enqueue")
	}

	ownerChanged := oldDeployment.DeepCopy()
	ownerChanged.OwnerReferences = nil
	if !p.Update(event.UpdateEvent{ObjectOld: oldDeployment, ObjectNew: ownerChanged}) {
		t.Fatal("Deployment ownerReference change should enqueue")
	}
}

func TestRelevantChildChangePredicateService(t *testing.T) {
	p := RelevantChildChangePredicate()
	oldService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8888}}},
	}

	annotationOnly := oldService.DeepCopy()
	annotationOnly.Annotations = map[string]string{"example.com/note": "keep"}
	if p.Update(event.UpdateEvent{ObjectOld: oldService, ObjectNew: annotationOnly}) {
		t.Fatal("irrelevant annotation-only Service update should not enqueue")
	}

	specChanged := oldService.DeepCopy()
	specChanged.Spec.Ports[0].Port = 8080
	if !p.Update(event.UpdateEvent{ObjectOld: oldService, ObjectNew: specChanged}) {
		t.Fatal("Service spec change should enqueue")
	}
}

func TestCollisionDeletePredicate(t *testing.T) {
	p := CollisionDeletePredicate()
	deployment := &appsv1.Deployment{}

	if p.Create(event.CreateEvent{Object: deployment}) {
		t.Fatal("collision create should not use the delete recovery mapper")
	}
	if p.Update(event.UpdateEvent{ObjectOld: deployment, ObjectNew: deployment.DeepCopy()}) {
		t.Fatal("collision update should not use the delete recovery mapper")
	}
	if !p.Delete(event.DeleteEvent{Object: deployment}) {
		t.Fatal("collision delete should reach the recovery mapper")
	}
	if p.Generic(event.GenericEvent{Object: deployment}) {
		t.Fatal("generic event should not use the collision recovery mapper")
	}
}
