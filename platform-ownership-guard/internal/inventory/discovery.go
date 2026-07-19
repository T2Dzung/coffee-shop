package inventory

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ResourceDiscovery is the narrow discovery capability required by inventory.
type ResourceDiscovery interface {
	ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error)
}

// DiscoveryHelper checks for API group and resource existence on the cluster.
type DiscoveryHelper struct {
	DiscoveryClient ResourceDiscovery
	RESTMapper      meta.RESTMapper
}

// NewDiscoveryHelper creates a new DiscoveryHelper.
func NewDiscoveryHelper(disc ResourceDiscovery, mapper meta.RESTMapper) *DiscoveryHelper {
	return &DiscoveryHelper{
		DiscoveryClient: disc,
		RESTMapper:      mapper,
	}
}

// IsArgoInstalled checks if the Argo CD Application CRD is registered and available.
func (h *DiscoveryHelper) IsArgoInstalled(ctx context.Context) (DiscoveryState, *ErrorDTO) {
	_ = ctx // discovery.Interface has no context-aware discovery method.
	gvk := schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "Application",
	}

	_, err := h.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if meta.IsNoMatchError(err) {
		h.tryResetMapper()
		_, err = h.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	}
	if err != nil {
		if meta.IsNoMatchError(err) {
			return DiscoveryUnavailable, &ErrorDTO{
				Class:   ErrDependencyUnavailable,
				Message: "Argo Application CRD is not installed on the cluster",
			}
		}

		if apierrors.IsForbidden(err) {
			return DiscoveryForbidden, &ErrorDTO{
				Class:   ErrEvidenceForbidden,
				Message: boundedMessage(fmt.Sprintf("Forbidden to query REST mapping for Argo Application: %v", err)),
			}
		}

		return DiscoveryUnknown, &ErrorDTO{
			Class:   ErrTransientReadFailure,
			Message: boundedMessage(fmt.Sprintf("Failed to resolve REST mapping: %v", err)),
		}
	}

	resources, err := h.DiscoveryClient.ServerResourcesForGroupVersion("argoproj.io/v1alpha1")
	if err != nil {
		if apierrors.IsNotFound(err) {
			h.tryResetMapper()
			return DiscoveryUnavailable, &ErrorDTO{
				Class:   ErrDependencyUnavailable,
				Message: "Argo groupversion argoproj.io/v1alpha1 not found in discovery",
			}
		}
		if apierrors.IsForbidden(err) {
			return DiscoveryForbidden, &ErrorDTO{
				Class:   ErrEvidenceForbidden,
				Message: boundedMessage(fmt.Sprintf("Forbidden to access discovery for Argo groupversion: %v", err)),
			}
		}
		return DiscoveryUnknown, &ErrorDTO{
			Class:   ErrTransientReadFailure,
			Message: boundedMessage(fmt.Sprintf("Failed to query discovery server: %v", err)),
		}
	}

	found := false
	for _, r := range resources.APIResources {
		if r.Kind == "Application" {
			found = true
			break
		}
	}

	if !found {
		h.tryResetMapper()
		return DiscoveryUnavailable, &ErrorDTO{
			Class:   ErrDependencyUnavailable,
			Message: "Argo Application resource not found in discovery",
		}
	}

	return DiscoveryAvailable, nil
}

func (h *DiscoveryHelper) tryResetMapper() {
	type resettable interface {
		Reset()
	}
	if r, ok := h.RESTMapper.(resettable); ok {
		r.Reset()
	}
}
