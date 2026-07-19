package controller

import (
	"context"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
)

type MutationRecord struct {
	Verb      string
	GVK       string
	Namespace string
	Name      string
	IsStatus  bool
	IsAudit   bool
}

type MutationRecorderClient struct {
	client.Client
	mu      sync.Mutex
	records []MutationRecord
}

func NewMutationRecorderClient(delegate client.Client) *MutationRecorderClient {
	return &MutationRecorderClient{Client: delegate}
}

func (m *MutationRecorderClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	m.record("create", obj, false)
	return m.Client.Create(ctx, obj, opts...)
}

func (m *MutationRecorderClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	m.record("update", obj, false)
	return m.Client.Update(ctx, obj, opts...)
}

func (m *MutationRecorderClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	m.record("patch", obj, false)
	return m.Client.Patch(ctx, obj, patch, opts...)
}

func (m *MutationRecorderClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	m.record("delete", obj, false)
	return m.Client.Delete(ctx, obj, opts...)
}

func (m *MutationRecorderClient) Status() client.SubResourceWriter {
	return &mutationRecorderStatusWriter{SubResourceWriter: m.Client.Status(), recorder: m}
}

func (m *MutationRecorderClient) record(verb string, obj client.Object, isStatus bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, isAudit := obj.(*guardplatformv1alpha1.OwnershipAudit)
	m.records = append(m.records, MutationRecord{
		Verb:      verb,
		GVK:       obj.GetObjectKind().GroupVersionKind().String(),
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
		IsStatus:  isStatus,
		IsAudit:   isAudit,
	})
}

func (m *MutationRecorderClient) Records() []MutationRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MutationRecord(nil), m.records...)
}

// AssertZeroTargetMutations permits only patch/update on OwnershipAudit/status.
func (m *MutationRecorderClient) AssertZeroTargetMutations() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, record := range m.records {
		allowedStatusWrite := record.IsStatus && record.IsAudit &&
			(record.Verb == "patch" || record.Verb == "update")
		if !allowedStatusWrite {
			return fmt.Errorf("forbidden mutation: verb=%s status=%t audit=%t resource=%s/%s gvk=%s",
				record.Verb, record.IsStatus, record.IsAudit, record.Namespace, record.Name, record.GVK)
		}
	}
	return nil
}

type mutationRecorderStatusWriter struct {
	client.SubResourceWriter
	recorder *MutationRecorderClient
}

func (w *mutationRecorderStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	w.recorder.record("update", obj, true)
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func (w *mutationRecorderStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	w.recorder.record("patch", obj, true)
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func (w *mutationRecorderStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	w.recorder.record("create", obj, true)
	return w.SubResourceWriter.Create(ctx, obj, subResource, opts...)
}
