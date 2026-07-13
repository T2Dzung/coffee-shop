package status

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

func TestSetConditionUsesGenerationAndTransitionSemantics(t *testing.T) {
	service := &platformv1alpha1.CoffeeShopService{ObjectMeta: metav1.ObjectMeta{Generation: 7}}
	firstTime := metav1.NewTime(time.Unix(100, 0))
	laterTime := metav1.NewTime(time.Unix(200, 0))

	if !SetCondition(service, ConditionReady, metav1.ConditionFalse, "Reconciling", "Waiting", firstTime) {
		t.Fatal("first condition should change status")
	}
	condition := service.Status.Conditions[0]
	if condition.ObservedGeneration != 7 {
		t.Fatalf("observedGeneration = %d", condition.ObservedGeneration)
	}
	if !condition.LastTransitionTime.Equal(&firstTime) {
		t.Fatal("first transition time was not used")
	}

	if SetCondition(service, ConditionReady, metav1.ConditionFalse, "Reconciling", "Waiting", laterTime) {
		t.Fatal("identical condition should be a no-op")
	}
	if !service.Status.Conditions[0].LastTransitionTime.Equal(&firstTime) {
		t.Fatal("no-op changed lastTransitionTime")
	}

	if !SetCondition(service, ConditionReady, metav1.ConditionFalse, "Reconciling", "Still waiting", laterTime) {
		t.Fatal("message update should change status")
	}
	if !service.Status.Conditions[0].LastTransitionTime.Equal(&firstTime) {
		t.Fatal("message-only update changed lastTransitionTime")
	}

	if !SetCondition(service, ConditionReady, metav1.ConditionTrue, "Reconciled", "Ready", laterTime) {
		t.Fatal("status transition should change status")
	}
	if !service.Status.Conditions[0].LastTransitionTime.Equal(&laterTime) {
		t.Fatal("status transition did not update lastTransitionTime")
	}
}
