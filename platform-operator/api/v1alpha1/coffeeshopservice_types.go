/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ManagementPolicy controls whether the future reconciler may mutate children.
// +kubebuilder:validation:Enum=Observe;Manage
type ManagementPolicy string

const (
	// ManagementPolicyObserve permits reads and status reporting only.
	ManagementPolicyObserve ManagementPolicy = "Observe"
	// ManagementPolicyManage permits reconciliation after ownership checks.
	ManagementPolicyManage ManagementPolicy = "Manage"
)

// AdoptionPolicy controls whether a same-name unowned resource may be adopted.
// +kubebuilder:validation:Enum=Never;Explicit
type AdoptionPolicy string

const (
	// AdoptionPolicyNever rejects unowned same-name resources.
	AdoptionPolicyNever AdoptionPolicy = "Never"
	// AdoptionPolicyExplicit requires the double opt-in ownership procedure.
	AdoptionPolicyExplicit AdoptionPolicy = "Explicit"
)

// ImageSpec identifies exactly one tag-addressed or digest-addressed image.
// +kubebuilder:validation:XValidation:rule="has(self.tag) != has(self.digest)",message="exactly one of tag or digest must be specified"
type ImageSpec struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Repository string `json:"repository"`

	// +optional
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`
	Tag string `json:"tag,omitempty"`

	// +optional
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest,omitempty"`

	// +optional
	// +kubebuilder:default=IfNotPresent
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`
}

// ContainerPortSpec declares a named container port.
type ContainerPortSpec struct {
	// +required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=15
	Name string `json:"name"`

	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ContainerPort int32 `json:"containerPort"`

	// +optional
	// +kubebuilder:default=TCP
	// +kubebuilder:validation:Enum=TCP;UDP;SCTP
	Protocol corev1.Protocol `json:"protocol,omitempty"`
}

// ServicePortSpec declares a ClusterIP Service port.
type ServicePortSpec struct {
	// +required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=15
	Name string `json:"name"`

	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// TargetPort references a declared container port by name.
	// +required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=15
	TargetPort string `json:"targetPort"`

	// +optional
	// +kubebuilder:default=TCP
	// +kubebuilder:validation:Enum=TCP;UDP;SCTP
	Protocol corev1.Protocol `json:"protocol,omitempty"`
}

// ServiceSpec configures the optional ClusterIP Service.
// +kubebuilder:validation:XValidation:rule="!self.enabled || (has(self.ports) && size(self.ports) > 0)",message="service ports must be declared when service is enabled"
type ServiceSpec struct {
	// +required
	Enabled bool `json:"enabled"`

	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	Ports []ServicePortSpec `json:"ports,omitempty"`
}

// ConfigMapKeySelectorSpec selects one ConfigMap key.
type ConfigMapKeySelectorSpec struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Key string `json:"key"`
}

// SecretKeySelectorSpec selects one Secret key without reading its data.
type SecretKeySelectorSpec struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Key string `json:"key"`
}

// EnvVarSourceSpec is a union of supported external value sources.
// +kubebuilder:validation:XValidation:rule="has(self.configMapKeyRef) != has(self.secretKeyRef)",message="exactly one of configMapKeyRef or secretKeyRef must be specified"
type EnvVarSourceSpec struct {
	// +optional
	ConfigMapKeyRef *ConfigMapKeySelectorSpec `json:"configMapKeyRef,omitempty"`
	// +optional
	SecretKeyRef *SecretKeySelectorSpec `json:"secretKeyRef,omitempty"`
}

// EnvVarSpec declares a literal or referenced environment variable.
// +kubebuilder:validation:XValidation:rule="has(self.value) != has(self.valueFrom)",message="exactly one of value or valueFrom must be specified"
type EnvVarSpec struct {
	// +required
	// +kubebuilder:validation:Pattern=`^[A-Za-z_][A-Za-z0-9_]*$`
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +optional
	// +kubebuilder:validation:MaxLength=4096
	Value *string `json:"value,omitempty"`
	// +optional
	ValueFrom *EnvVarSourceSpec `json:"valueFrom,omitempty"`
}

// HTTPGetAction is the safe HTTP probe subset supported by v0.1.
type HTTPGetAction struct {
	// +optional
	// +kubebuilder:default="/"
	// +kubebuilder:validation:Pattern=`^/.*`
	Path string `json:"path,omitempty"`
	// +required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=15
	Port string `json:"port"`
	// +optional
	// +kubebuilder:default=HTTP
	// +kubebuilder:validation:Enum=HTTP;HTTPS
	Scheme corev1.URIScheme `json:"scheme,omitempty"`
}

// TCPSocketAction is the safe TCP probe subset supported by v0.1.
type TCPSocketAction struct {
	// +required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=15
	Port string `json:"port"`
}

// GRPCAction configures a Kubernetes gRPC health probe.
type GRPCAction struct {
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Service string `json:"service,omitempty"`
}

// ProbeSpec excludes exec, host and arbitrary headers and accepts one handler.
// +kubebuilder:validation:XValidation:rule="(has(self.httpGet) ? 1 : 0) + (has(self.tcpSocket) ? 1 : 0) + (has(self.grpc) ? 1 : 0) == 1",message="exactly one of httpGet, tcpSocket or grpc must be specified"
type ProbeSpec struct {
	// +optional
	HTTPGet *HTTPGetAction `json:"httpGet,omitempty"`
	// +optional
	TCPSocket *TCPSocketAction `json:"tcpSocket,omitempty"`
	// +optional
	GRPC *GRPCAction `json:"grpc,omitempty"`
	// +optional
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=300
	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=60
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
	// +optional
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=300
	PeriodSeconds int32 `json:"periodSeconds,omitempty"`
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	SuccessThreshold int32 `json:"successThreshold,omitempty"`
	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=60
	FailureThreshold int32 `json:"failureThreshold,omitempty"`
}

// ProbesSpec configures supported lifecycle probes.
type ProbesSpec struct {
	// +optional
	Startup *ProbeSpec `json:"startup,omitempty"`
	// +optional
	Readiness *ProbeSpec `json:"readiness,omitempty"`
	// +optional
	Liveness *ProbeSpec `json:"liveness,omitempty"`
}

// PDBSpec intentionally excludes percentages so CEL can compare it to replicas.
type PDBSpec struct {
	// +required
	Enabled bool `json:"enabled"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	MinAvailable *int32 `json:"minAvailable,omitempty"`
}

// TopologySpreadSpec configures fixed hostname spreading for v0.1.
type TopologySpreadSpec struct {
	// +required
	Enabled bool `json:"enabled"`
	// +optional
	// +kubebuilder:default=ScheduleAnyway
	// +kubebuilder:validation:Enum=DoNotSchedule;ScheduleAnyway
	WhenUnsatisfiable corev1.UnsatisfiableConstraintAction `json:"whenUnsatisfiable,omitempty"`
}

// AvailabilitySpec groups Phase 6.3 availability guardrails.
type AvailabilitySpec struct {
	// +optional
	PDB *PDBSpec `json:"pdb,omitempty"`
	// +optional
	TopologySpread *TopologySpreadSpec `json:"topologySpread,omitempty"`
}

// EgressRuleSpec declares one same-namespace stateless dependency.
type EgressRuleSpec struct {
	// +required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Service string `json:"service"`
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
	// +optional
	// +kubebuilder:default=TCP
	// +kubebuilder:validation:Enum=TCP;UDP;SCTP
	Protocol corev1.Protocol `json:"protocol,omitempty"`
}

// NetworkPolicySpec configures Phase 6.4 dependency policies.
type NetworkPolicySpec struct {
	// +required
	Enabled bool `json:"enabled"`
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=service
	// +listMapKey=port
	Egress []EgressRuleSpec `json:"egress,omitempty"`
}

// CoffeeShopServiceSpec defines the stateless workload contract.
// +kubebuilder:validation:XValidation:rule="!has(self.service) || !self.service.enabled || (has(self.ports) && self.service.ports.all(sp, self.ports.exists(cp, cp.name == sp.targetPort)))",message="every service targetPort must reference a declared container port name"
// +kubebuilder:validation:XValidation:rule="!has(self.availability) || !has(self.availability.pdb) || !self.availability.pdb.enabled || (self.replicas >= 2 && has(self.availability.pdb.minAvailable) && self.availability.pdb.minAvailable < self.replicas)",message="an enabled PDB requires replicas >= 2 and minAvailable between 1 and replicas-1"
// +kubebuilder:validation:XValidation:rule="has(self.resources.requests) && has(self.resources.limits) && 'cpu' in self.resources.requests && 'memory' in self.resources.requests && 'cpu' in self.resources.limits && 'memory' in self.resources.limits",message="cpu and memory requests and limits are required"
type CoffeeShopServiceSpec struct {
	// +required
	ManagementPolicy ManagementPolicy `json:"managementPolicy"`
	// +optional
	// +kubebuilder:default=Never
	AdoptionPolicy AdoptionPolicy `json:"adoptionPolicy,omitempty"`
	// +required
	Image ImageSpec `json:"image"`
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	Replicas int32 `json:"replicas"`
	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	Ports []ContainerPortSpec `json:"ports,omitempty"`
	// +optional
	Service *ServiceSpec `json:"service,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	Env []EnvVarSpec `json:"env,omitempty"`
	// +required
	Resources corev1.ResourceRequirements `json:"resources"`
	// +optional
	Probes *ProbesSpec `json:"probes,omitempty"`
	// +optional
	Availability *AvailabilitySpec `json:"availability,omitempty"`
	// +optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`
}

// CoffeeShopServiceStatus defines the observed state reported in Phase 6.2.
type CoffeeShopServiceStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Desired",type="integer",JSONPath=".status.desiredReplicas"
// +kubebuilder:printcolumn:name="Available",type="integer",JSONPath=".status.readyReplicas"
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.image.repository"
// +kubebuilder:printcolumn:name="Management",type="string",JSONPath=".spec.managementPolicy"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// CoffeeShopService is the Schema for the coffeeshopservices API.
type CoffeeShopService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              CoffeeShopServiceSpec   `json:"spec"`
	Status            CoffeeShopServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CoffeeShopServiceList contains a list of CoffeeShopService.
type CoffeeShopServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CoffeeShopService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CoffeeShopService{}, &CoffeeShopServiceList{})
}
