package inventory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
)

type registryEntry struct {
	ObjectGVK schema.GroupVersionKind
	ListGVK   schema.GroupVersionKind
}

var supportedRegistry = map[schema.GroupVersionKind]registryEntry{
	{Group: "apps", Version: "v1", Kind: "Deployment"}: {ObjectGVK: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, ListGVK: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DeploymentList"}},
	{Group: "apps", Version: "v1", Kind: "ReplicaSet"}: {ObjectGVK: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}, ListGVK: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSetList"}},
}

// CollectorOption configures an explicit read capability.
type CollectorOption func(*Collector)

// WithAuthoritativeOwnerReader permits bounded direct owner confirmation.
func WithAuthoritativeOwnerReader(reader client.Reader) CollectorOption {
	return func(c *Collector) { c.AuthoritativeOwnerReader = reader }
}

// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get
// +kubebuilder:rbac:groups=apps,resources=deployments;replicasets,verbs=get;list;watch

// InventoryCollector collects normalized evidence from the cluster.
type InventoryCollector interface {
	Collect(ctx context.Context, targetNamespace string, spec *guardplatformv1alpha1.OwnershipAuditSpec) (*NormalizedSnapshot, error)
}

// Collector implements the InventoryCollector interface.
type Collector struct {
	Reader                   client.Reader
	AuthoritativeOwnerReader client.Reader
	DynamicClient            dynamic.Interface
	DiscoveryHelper          *DiscoveryHelper
	Parser                   *SafeParser
	Now                      func() time.Time
}

// NewCollector creates a new Collector.
func NewCollector(reader client.Reader, dyn dynamic.Interface, disc *DiscoveryHelper, options ...CollectorOption) *Collector {
	collector := &Collector{
		Reader:          reader,
		DynamicClient:   dyn,
		DiscoveryHelper: disc,
		Parser:          NewSafeParser(),
		Now:             time.Now,
	}
	for _, option := range options {
		option(collector)
	}
	return collector
}

// Collect reads dynamic Argo resources and target resources from the cluster and normalizes them.
func (c *Collector) Collect(ctx context.Context, targetNamespace string, spec *guardplatformv1alpha1.OwnershipAuditSpec) (*NormalizedSnapshot, error) {
	snapshot := &NormalizedSnapshot{
		ObservedAt:      c.Now(),
		TargetNamespace: targetNamespace,
	}

	// 1. Enforce GVK supported registry validation
	for _, rule := range spec.TargetRules {
		if !c.isGVKSupported(rule.APIGroup, rule.Version, rule.Kind) {
			message := fmt.Sprintf("unsupported target GVK: %s/%s, Kind=%s", rule.APIGroup, rule.Version, rule.Kind)
			return nil, &InventoryError{DTO: ErrorDTO{Class: ErrInvalidInventoryScope, Message: message}}
		}
	}

	// 2. Discover Argo CD installation state
	argoState, errDTO := c.DiscoveryHelper.IsArgoInstalled(ctx)
	snapshot.ArgoDiscoveryState = argoState
	if errDTO != nil {
		snapshot.ArgoDiscoveryError = errDTO
		if errDTO.Class == ErrTransientReadFailure {
			return snapshot, &InventoryError{DTO: *errDTO}
		}
		return snapshot, nil
	}

	// 3. Read Argo Application evidence (bounded at max 20)
	appCount := len(spec.ApplicationRefs)
	if appCount > 20 {
		appCount = 20
	}

	gvr := schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "applications",
	}

	var transientErr error
	for i := 0; i < appCount; i++ {
		ref := spec.ApplicationRefs[i]
		metadata := ObservationMetadata{ObservedAt: c.Now(), Freshness: FreshnessUnknown}

		unstr, err := c.DynamicClient.Resource(gvr).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			errDTO := c.classifyError(err)
			metadata.SourceError = errDTO
			if errDTO.Class == ErrTransientReadFailure && transientErr == nil {
				transientErr = &InventoryError{DTO: *errDTO}
			}
			snapshot.Applications = append(snapshot.Applications, ApplicationEvidence{
				ApplicationRef: ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: ref.Namespace,
					Name:      ref.Name,
				},
				Metadata: metadata,
			})
			continue
		}

		metadata.SourceResourceVersion = unstr.GetResourceVersion()

		appEvidence, err := c.Parser.ParseApplication(unstr)
		if err != nil {
			metadata.SourceError = c.newErrorDTO(ErrMalformedEvidence, fmt.Sprintf("application evidence malformed: %v", err))
			snapshot.Applications = append(snapshot.Applications, ApplicationEvidence{
				ApplicationRef: ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: ref.Namespace,
					Name:      ref.Name,
				},
				Metadata: metadata,
			})
			continue
		}

		metadata.SourceObservedAt = appEvidence.Metadata.SourceObservedAt
		metadata.Freshness = c.freshnessFor(metadata.SourceObservedAt, spec.ResyncInterval.Duration)
		appEvidence.Metadata = metadata
		snapshot.Applications = append(snapshot.Applications, *appEvidence)
	}

	// 4. Collect target protections and owner evidences in targetNamespace
	for _, rule := range spec.TargetRules {
		gvk := schema.GroupVersionKind{
			Group:   rule.APIGroup,
			Version: rule.Version,
			Kind:    rule.Kind,
		}

		entry := supportedRegistry[gvk]
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(entry.ListGVK)

		err := c.Reader.List(ctx, list, client.InNamespace(targetNamespace))
		if err != nil {
			errDTO := c.classifyError(err)
			if errDTO.Class == ErrTransientReadFailure && transientErr == nil {
				transientErr = &InventoryError{DTO: *errDTO}
			}
			snapshot.Protections = append(snapshot.Protections, ProtectionEvidence{
				TargetRef: ResourceIdentity{
					APIGroup:  rule.APIGroup,
					Version:   rule.Version,
					Kind:      rule.Kind,
					Namespace: targetNamespace,
				},
				Metadata: ObservationMetadata{
					ObservedAt:  c.Now(),
					SourceError: errDTO,
				},
				Readable: false,
			})
			continue
		}

		for _, item := range list.Items {
			if item.GroupVersionKind().Empty() || strings.HasSuffix(item.GetKind(), "List") {
				item.SetGroupVersionKind(entry.ObjectGVK)
			}
			prot := c.Parser.ParseProtection(&item)
			prot.Metadata.ObservedAt = c.Now()
			prot.Metadata.SourceResourceVersion = item.GetResourceVersion()
			prot.Metadata.Freshness = FreshnessFresh
			snapshot.Protections = append(snapshot.Protections, *prot)

			ownerRefs := item.GetOwnerReferences()
			for _, ownerRef := range ownerRefs {
				ownerEvidence := OwnerEvidence{
					DependentIdentity: ResourceIdentity{
						APIGroup:  item.GroupVersionKind().Group,
						Version:   item.GroupVersionKind().Version,
						Kind:      item.GroupVersionKind().Kind,
						Namespace: item.GetNamespace(),
						Name:      item.GetName(),
						UID:       item.GetUID(),
					},
					Metadata: ObservationMetadata{
						ObservedAt:            c.Now(),
						SourceResourceVersion: item.GetResourceVersion(),
						Freshness:             FreshnessFresh,
					},
					OwnerRefGVK: schema.FromAPIVersionAndKind(ownerRef.APIVersion, ownerRef.Kind),
					OwnerName:   ownerRef.Name,
					OwnerUID:    ownerRef.UID,
				}

				ownerGVK := schema.FromAPIVersionAndKind(ownerRef.APIVersion, ownerRef.Kind)
				if c.isGVKSupported(ownerGVK.Group, ownerGVK.Version, ownerGVK.Kind) {
					ownerObj := &unstructured.Unstructured{}
					ownerObj.SetGroupVersionKind(ownerGVK)

					err := c.Reader.Get(ctx, client.ObjectKey{Namespace: item.GetNamespace(), Name: ownerRef.Name}, ownerObj)
					if err == nil {
						ownerEvidence.LookupResult = OwnerResolved
						ownerEvidence.ObservedOwnerUID = ownerObj.GetUID()
					} else {
						if apierrors.IsNotFound(err) {
							ownerEvidence.LookupResult = OwnerUnknown
							ownerEvidence.Metadata.Freshness = FreshnessUnknown
							ownerEvidence.Metadata.SourceError = c.newErrorDTO(ErrStaleEvidence, "cached owner lookup missed; authoritative confirmation required")
							if c.AuthoritativeOwnerReader != nil {
								authoritative := &unstructured.Unstructured{}
								authoritative.SetGroupVersionKind(ownerGVK)
								authErr := c.AuthoritativeOwnerReader.Get(ctx, client.ObjectKey{Namespace: item.GetNamespace(), Name: ownerRef.Name}, authoritative)
								if authErr == nil {
									ownerEvidence.LookupResult = OwnerResolved
									ownerEvidence.ObservedOwnerUID = authoritative.GetUID()
									ownerEvidence.Metadata.Freshness = FreshnessFresh
									ownerEvidence.Metadata.SourceError = nil
								} else if apierrors.IsNotFound(authErr) {
									ownerEvidence.LookupResult = OwnerNotFound
									ownerEvidence.Metadata.Freshness = FreshnessFresh
									ownerEvidence.Metadata.SourceError = nil
								} else {
									ownerEvidence.Metadata.SourceError = c.classifyError(authErr)
								}
							}
						} else if apierrors.IsForbidden(err) {
							ownerEvidence.LookupResult = OwnerForbidden
						} else {
							ownerEvidence.LookupResult = OwnerUnknown
							ownerEvidence.Metadata.SourceError = c.classifyError(err)
						}
					}
				} else {
					ownerEvidence.LookupResult = OwnerUnknown
				}

				snapshot.Owners = append(snapshot.Owners, ownerEvidence)
			}
		}
	}

	c.sortSnapshot(snapshot)
	return snapshot, transientErr
}

func (c *Collector) isGVKSupported(group, version, kind string) bool {
	_, ok := supportedRegistry[schema.GroupVersionKind{Group: group, Version: version, Kind: kind}]
	return ok
}

func (c *Collector) freshnessFor(sourceObservedAt *time.Time, threshold time.Duration) FreshnessState {
	if sourceObservedAt == nil {
		return FreshnessUnknown
	}
	if threshold <= 0 {
		threshold = 10 * time.Minute
	}
	if c.Now().Sub(*sourceObservedAt) > threshold {
		return FreshnessStale
	}
	return FreshnessFresh
}

func (c *Collector) sortSnapshot(snapshot *NormalizedSnapshot) {
	sort.Slice(snapshot.Applications, func(i, j int) bool {
		return identityKey(snapshot.Applications[i].ApplicationRef) < identityKey(snapshot.Applications[j].ApplicationRef)
	})
	sort.Slice(snapshot.Protections, func(i, j int) bool {
		return identityKey(snapshot.Protections[i].TargetRef) < identityKey(snapshot.Protections[j].TargetRef)
	})
	sort.Slice(snapshot.Owners, func(i, j int) bool {
		left := identityKey(snapshot.Owners[i].DependentIdentity) + "/" + snapshot.Owners[i].OwnerRefGVK.String() + "/" + snapshot.Owners[i].OwnerName
		right := identityKey(snapshot.Owners[j].DependentIdentity) + "/" + snapshot.Owners[j].OwnerRefGVK.String() + "/" + snapshot.Owners[j].OwnerName
		return left < right
	})
}

func identityKey(ref ResourceIdentity) string {
	return ref.APIGroup + "/" + ref.Version + "/" + ref.Kind + "/" + ref.Namespace + "/" + ref.Name
}

func (c *Collector) newErrorDTO(class ErrorClass, message string) *ErrorDTO {
	message = boundedMessage(message)
	return &ErrorDTO{Class: class, Message: message}
}

func (c *Collector) classifyError(err error) *ErrorDTO {
	if err == nil {
		return nil
	}

	if apierrors.IsForbidden(err) {
		return &ErrorDTO{
			Class:   ErrEvidenceForbidden,
			Message: err.Error(),
		}
	}
	if apierrors.IsNotFound(err) {
		return &ErrorDTO{
			Class:   ErrDependencyUnavailable,
			Message: err.Error(),
		}
	}
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) {
		return &ErrorDTO{
			Class:   ErrTransientReadFailure,
			Message: err.Error(),
		}
	}

	return c.newErrorDTO(ErrTransientReadFailure, err.Error())
}
