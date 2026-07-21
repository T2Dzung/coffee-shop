# PlatformOwnershipGuard

A read-only Kubernetes operator that continuously audits resource ownership across GitOps-managed clusters. It detects unprotected Argo CD prune candidates and stale owner references, then surfaces findings through status conditions, Kubernetes Events and bounded Prometheus metrics — without ever modifying the resources it inspects.

## Why this exists

In a GitOps-managed cluster with multiple resource sources (Argo CD, Ansible, Helm, manual apply), resources can silently become prune candidates or lose their intended owners. A single misconfigured annotation or a recreated Secret can lead to data loss on the next Argo CD sync. PlatformOwnershipGuard provides an automated, continuous safety net that catches these issues before they cause outages.

**Origin story:** This project was born from a real incident where a RabbitMQ connection Secret — created outside Git but tracked by Argo CD — was marked `requiresPruning=true` and nearly deleted during a routine sync, which would have taken down the entire message queue layer.

## What it does

```
Kubernetes API  ──read──▶  Collector  ──▶  Pure Detectors  ──▶  OwnershipAudit.status
                                              │                         │
                                              │                    Kubernetes Events
                                              │                    Prometheus metrics
                                              ▼
                                    Zero writes to target
                                    Deployments / Secrets / Apps
```

- **ArgoPruneRisk detector** — flags resources marked for pruning that lack `Prune=false` protection, with confidence levels based on available evidence.
- **StaleOwnerReference detector** — identifies resources pointing to owners that no longer exist or whose UIDs have changed.
- **Status-first signal pipeline** — persisted-status diff prevents Event/metric storms on process restarts.
- **Bounded observability** — four Prometheus metric families with strictly enum labels; no object-name or UID cardinality risk.

## Key design decisions

| Decision | Rationale |
|---|---|
| **Read-only by design** | Detectors are pure functions with no Kubernetes client. RBAC enforces read-only access to target resources. Envtest mutation recorder verifies zero writes in CI. |
| **Evidence-graded findings** | Each finding carries Severity (impact) and Confidence (evidence quality). Missing evidence yields `Suspected` or `InsufficientEvidence`, never false `Confirmed`. |
| **Active-passive HA** | Two replicas with controller-runtime Lease-based leader election. Namespaced Lease RBAC keeps coordination scope separate from audit permissions. |
| **Immutable supply chain** | Release images are Trivy-scanned, SBOM-generated, and Cosign-signed by digest. GitOps deployment pins the exact digest — the scanned artifact is the running artifact. |
| **Git-only lifecycle** | All rollouts, rollbacks and disable/recovery go through Git commits. No `kubectl rollout undo` or manual live patches. |

## Current state (v0.1 DEV release candidate)

Verified on a self-managed EC2/K3s HA cluster with Argo CD, Prometheus and Grafana. Evidence includes:

- ~16h40m shadow observation (191 scans, zero errors/restarts/transitions)
- Pod-level leader failover in 20 seconds (SLO ≤ 30s)
- Exact-digest upgrade/rollback compatibility (N-1 → N → N-1)
- Negative RBAC verification on live cluster (target writes denied)
- Target workload fingerprint unchanged across failover

**Not yet proven:** node-loss failover, AWS EKS deployment, full CRD uninstall/reinstall under load, multi-cluster operation, auto-remediation.

## Quick start

```bash
# Run unit and integration tests (requires envtest binaries)
cd platform-ownership-guard
make test

# Build the operator binary
go build -o bin/manager cmd/main.go

# Validate infrastructure contracts (from repository root)
bash scripts/validate-infra.sh
```

For GitOps deployment, the release workflow handles image build, scanning, signing and digest pinning automatically on push to the tracked branch.

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
│   └── samples/           # OwnershipAudit example
└── scripts/
    └── rehearsal/          # Failover, upgrade/rollback, removal helpers
```

## License

Copyright 2026. Licensed under the Apache License, Version 2.0. See the repository root LICENSE file for full terms.
