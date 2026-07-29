# PlatformOwnershipGuard

A read-only Kubernetes operator that continuously audits resource ownership across GitOps-managed clusters. It detects unprotected Argo CD prune candidates and stale owner references, then surfaces findings through status conditions, Kubernetes Events and bounded Prometheus metrics — without modifying the resources it inspects.

## Why this exists

In a GitOps-managed cluster with multiple resource sources (Argo CD, Ansible, Helm, manual apply), resources can silently become prune candidates or lose their intended owners. A misconfigured tracking annotation or a recreated owner can make a later lifecycle operation unsafe. PlatformOwnershipGuard records bounded signals so an operator can review the ownership problem and repair the source of truth.

**Origin story:** This project followed an incident in which a RabbitMQ connection Secret created outside Git was tracked by Argo CD and appeared as a prune candidate. The detector focuses on the observable ownership risk; it does not claim to prevent every unsafe sync.

## What it does

```
Kubernetes API  ──read──▶  Collector  ──▶  Pure Detectors  ──▶  OwnershipAudit.status
                                              │                         │
                                              │                    Kubernetes Events
                                              │                    Prometheus metrics
                                              ▼
                                    Zero writes to audited targets
```

- **ArgoPruneRisk detector** — flags resources marked for pruning that lack `Prune=false` protection, with confidence levels based on available evidence.
- **StaleOwnerReference detector** — identifies resources pointing to owners that no longer exist or whose UIDs have changed.
- **Capability-based collection** — the same detector pipeline handles built-in Kubernetes resources and explicitly registered CRDs; detector code does not branch on ESO or cert-manager.
- **Status-first signal pipeline** — persisted-status diff prevents Event/metric storms on process restarts.
- **Bounded observability** — four Prometheus metric families with strictly enum labels; no object-name or UID cardinality risk.

## Supported evidence

| API resource | Used as | Required detector dependency |
|---|---|---|
| `apps/v1` `Deployment`, `ReplicaSet` | Audited targets and owner identity | Selected detector only |
| `external-secrets.io/v1` `ExternalSecret` | Audited target for Argo prune correlation | `ArgoPruneRisk` |
| `cert-manager.io/v1` `CertificateRequest` | Audited dependent with owner references | `StaleOwnerReference` |
| `cert-manager.io/v1` `Certificate` | Owner identity and UID resolution | `StaleOwnerReference` |
| `argoproj.io/v1alpha1` `Application` | Explicitly selected GitOps evidence | `ArgoPruneRisk` only |

An audit that enables only `StaleOwnerReference` does not discover or read Argo CD and
does not require `spec.applicationRefs`. The registry and generated RBAC remain explicit:
there are no wildcard resource permissions and the operator never receives Secret access.

## Key design decisions

| Decision | Rationale |
|---|---|
| **Read-only by design** | Detectors are pure functions with no Kubernetes client. RBAC enforces read-only access to target resources. Envtest mutation recorder verifies zero writes in CI. |
| **Evidence-graded findings** | Each finding carries Severity (impact) and Confidence (evidence quality). Missing evidence yields `Suspected` or `InsufficientEvidence`, never false `Confirmed`. |
| **Active-passive HA** | Two replicas with controller-runtime Lease-based leader election. Namespaced Lease RBAC keeps coordination scope separate from audit permissions. |
| **Immutable supply chain** | Release images are Trivy-scanned, SBOM-generated, and Cosign-signed by digest. GitOps deployment pins the exact digest — the scanned artifact is the running artifact. |
| **Git-only lifecycle** | All rollouts, rollbacks and disable/recovery go through Git commits. No `kubectl rollout undo` or manual live patches. |

## Deployment model

The operator ships with DEV and PROD Kustomize overlays. It runs two replicas with
leader election, reads the resources selected by an `OwnershipAudit`, and writes only
the audit status, Events and Prometheus metrics. It reports ownership risks; it is not
an admission controller and does not repair or delete audited resources.

## Quick start

```bash
# Run unit and integration tests (requires envtest binaries)
cd platform-ownership-guard
make test

# Run the disposable no-Argo deployment/RBAC test (requires Docker and Kind)
bash test/e2e/kind_no_argo.sh

# Build the operator binary
go build -o bin/manager cmd/main.go

# Validate infrastructure contracts (from repository root)
bash scripts/validate-infra.sh
```

After a change reaches the protected default branch, the standard release workflow can
build, scan and sign the operator image and pin its digest through the same DEV/QA/PROD
delivery path as the application services.

## Project structure

```
platform-ownership-guard/
├── api/v1alpha1/          # CRD types (OwnershipAudit)
├── cmd/main.go            # Manager entry point with leader election
├── internal/
│   ├── controller/        # Reconciler, status builder, transition engine
│   ├── detectors/         # Pure evaluation functions + stable identity
│   ├── inventory/         # API evidence collector and normalizer
│   ├── telemetry/         # Bounded Prometheus metrics
│   └── security/          # RBAC contract tests
├── config/
│   ├── crd/               # Generated CRD manifests
│   ├── rbac/              # ClusterRole (audit) + Role (Lease)
│   ├── manager/           # Deployment, PDB
│   ├── observability/     # Service, ServiceMonitor, alerts, dashboard
│   ├── dev/               # DEV overlay with pinned digest
│   ├── prod/              # PROD overlay with pinned digest
│   └── samples/           # OwnershipAudit example
└── scripts/
    └── rehearsal/          # Failover, upgrade/rollback, removal helpers
```

## License

See the repository root [MIT license](../LICENSE). This README does not replace or alter the copyright notices in the source files.
