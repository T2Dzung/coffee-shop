package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

func TestClassifyOwnership(t *testing.T) {
	controller := true
	parent := &platformv1alpha1.CoffeeShopService{
		ObjectMeta: metav1.ObjectMeta{Name: "web", UID: types.UID("current-uid")},
	}

	tests := []struct {
		name   string
		exists bool
		refs   []metav1.OwnerReference
		want   OwnershipResult
	}{
		{name: "absent", exists: false, want: OwnershipAbsent},
		{name: "unowned", exists: true, want: OwnershipUnownedCollision},
		{
			name:   "owned",
			exists: true,
			refs: []metav1.OwnerReference{{
				APIVersion: platformv1alpha1.GroupVersion.String(),
				Kind:       "CoffeeShopService",
				Name:       parent.Name,
				UID:        parent.UID,
				Controller: &controller,
			}},
			want: OwnershipOwned,
		},
		{
			name:   "foreign kind",
			exists: true,
			refs: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "foreign",
				UID:        types.UID("foreign-uid"),
				Controller: &controller,
			}},
			want: OwnershipForeignOwnedCollision,
		},
		{
			name:   "stale UID",
			exists: true,
			refs: []metav1.OwnerReference{{
				APIVersion: platformv1alpha1.GroupVersion.String(),
				Kind:       "CoffeeShopService",
				Name:       parent.Name,
				UID:        types.UID("stale-uid"),
				Controller: &controller,
			}},
			want: OwnershipStaleOwnerCollision,
		},
		{
			name:   "different CoffeeShopService",
			exists: true,
			refs: []metav1.OwnerReference{{
				APIVersion: platformv1alpha1.GroupVersion.String(),
				Kind:       "CoffeeShopService",
				Name:       "proxy",
				UID:        types.UID("proxy-uid"),
				Controller: &controller,
			}},
			want: OwnershipForeignOwnedCollision,
		},
		{
			name:   "non-controller reference only",
			exists: true,
			refs: []metav1.OwnerReference{{
				APIVersion: platformv1alpha1.GroupVersion.String(),
				Kind:       "CoffeeShopService",
				Name:       parent.Name,
				UID:        parent.UID,
			}},
			want: OwnershipUnownedCollision,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyOwnership(parent, tt.refs, tt.exists); got != tt.want {
				t.Fatalf("ClassifyOwnership() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDesiredOwnerReferenceUsesControllerWithoutDeletionBlock(t *testing.T) {
	parent := &platformv1alpha1.CoffeeShopService{
		ObjectMeta: metav1.ObjectMeta{Name: "web", UID: types.UID("current-uid")},
	}

	ref := DesiredOwnerReference(parent)
	if ref.Controller == nil || !*ref.Controller {
		t.Fatal("Controller must be true")
	}
	if ref.BlockOwnerDeletion != nil {
		t.Fatalf("BlockOwnerDeletion = %v, want nil to preserve least-privilege RBAC", *ref.BlockOwnerDeletion)
	}
	if ref.UID != parent.UID {
		t.Fatalf("UID = %q, want %q", ref.UID, parent.UID)
	}
}
