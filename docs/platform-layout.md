# Infrastructure and platform code layout

This document maps the public implementation by responsibility. It is the navigation
guide for contributors and operators.

## Top-level ownership

```text
go-coffeeshop-main/
├── .github/
│   ├── actions/                 Reusable delivery leaf operations
│   └── workflows/               CI, candidate, DEV, QA, PROD and rollback entrypoints
├── cmd/
│   ├── platformctl/             Public platform lifecycle CLI
│   └── <service>/               Upstream CoffeeShop application binaries
├── internal/
│   ├── platformctl/             Typed platform orchestration and adapters
│   └── <service>/               Upstream application implementation
├── infrastructure/
│   ├── ansible/                 Self-managed DEV host and K3s configuration
│   ├── k8s/                     Shared workload plus DEV/PROD GitOps desired state
│   ├── terraform/               Cloud foundation, environments and reusable modules
│   ├── release-candidates/      Immutable candidate and QA records
│   └── releases/                Environment release records and PROD release sets
├── platform/                    Component, toolchain and operator metadata
├── platform-ownership-guard/    Independent Kubernetes operator module
├── policy/                      Pure semantic Rego policies and fixtures
└── scripts/                     Thin wrappers and deterministic verification/rehearsal leaves
```

## Platform CLI and Go packages

`cmd/platformctl/main.go` parses public commands and wires dependencies. Domain behavior
lives under `internal/platformctl`:

| Package | Responsibility |
| --- | --- |
| `config` | Typed DEV, CI, PROD, GitHub and operator configuration with validation and precedence |
| `dev` | DEV setup, status, teardown, approval and retained-recovery verification |
| `ci` | Ephemeral CI infrastructure lifecycle and failure boundaries |
| `prod` | PROD bootstrap, setup, verify, release gates, resilience checks, isolated RDS restore drill and teardown |
| `github` | Repository governance lifecycle and approval boundary |
| `release` | Candidate, DEV, QA and PROD artifact identity contracts |
| `component` | Typed reader for `platform/components.yaml` and change selection |
| `terraform` | Saved-plan execution, plan summaries and remote S3 backend rendering |
| `aws` | Bounded AWS CLI adapter and pricing lookup |
| `kubernetes` | `kubectl` adapter and Kubernetes-facing execution boundary |
| `gitops` | Argo CD/application status interpretation |
| `policy` | Structured Conftest/Rego evaluation adapter |
| `command` | Shared process runner, context, output and fake-runner test seam |
| `evidence` | Structured command/evidence output helpers |
| `toolchain` | Versioned external-tool contract from platform metadata |
| `validation` | Cross-artifact validation composition without owning domain behavior |

The dependency direction is intentionally one-way:

```text
GitHub workflow or thin Bash wrapper
  → cmd/platformctl
  → DEV / CI / PROD / release state machine
  → typed Terraform / AWS / Kubernetes / GitOps / policy adapter
  → command.Runner
  → native external tool
```

## Infrastructure tree

### Terraform

```text
infrastructure/terraform/
├── bootstrap/
│   ├── dev/                     DEV backend bootstrap
│   └── prod/                    PROD backend and protection bootstrap
├── envs/
│   ├── dev/                     EC2/K3s, API endpoint, ECR, IAM, Longhorn EBS and backup S3
│   ├── ci/                      Ephemeral ARC/CI compute and networking
│   └── prod/                    VPC, EKS, managed nodes, ECR, RDS, IAM, secrets, Synthetics and alarms
├── github/                      GitHub environments, variables and protection governance
└── modules/
    ├── ec2-instance/            Reusable DEV/CI compute
    ├── eks-cluster/             EKS control-plane boundary
    ├── eks-managed-node/        EKS managed node group
    ├── terraform-backend/       Versioned, encrypted remote state
    └── vpc/                     Environment network foundation
```

Each environment keeps a separate backend/state key and environment-specific IAM,
inventory and teardown boundary. Reuse is limited to mechanisms, not environment state.

### Ansible

```text
infrastructure/ansible/
├── inventory/                   AWS dynamic inventory for self-managed nodes
├── playbooks/                   DEV bootstrap and CI runner entrypoints
├── roles/
│   ├── preflight, common        Host prerequisites and shared baseline
│   ├── k3s_*                    First-server, joining-server and cluster verification
│   ├── cilium                   eBPF networking and kube-proxy replacement
│   ├── haproxy_lb               Stable K3s API endpoint
│   ├── longhorn_prereqs         Storage host preparation
│   ├── argocd                   GitOps controller bootstrap
│   ├── arc_runner               Actions Runner Controller configuration
│   ├── coffeeshop_*             Runtime app, PostgreSQL, RabbitMQ and secret bootstrap
│   └── monitoring_secrets       Observability credential bootstrap
└── templates/                   Host and Kubernetes bootstrap templates
```

Ansible owns host configuration and bootstrap ordering for the self-managed DEV
environment. Long-term Kubernetes desired state moves to Argo CD after bootstrap.

### Kubernetes and GitOps

```text
infrastructure/k8s/
├── apps/coffeeshop/
│   ├── base/                    Portable application workloads
│   └── overlays/
│       ├── dev/                 DEV digests, CNPG binding and HPA ownership
│       └── prod/                PROD digests, RDS bootstrap/migration and ALB ingress
├── ci/arc/                      Actions Runner Controller workload definitions
└── environments/
    ├── dev/
    │   ├── bootstrap/           Argo CD root applications
    │   ├── gitops/applications/ Add-on Applications and ordering
    │   ├── gitops/addons/       Cilium-adjacent, storage and observability values
    │   ├── gitops/apps/         CNPG and RabbitMQ custom resources
    │   ├── network/             Gateway and routing resources
    │   └── policies/            Default-deny and explicit allow rules
    └── prod/
        ├── bootstrap/           Bounded AppProject and Argo CD Applications
        └── platform/            EBS, External Secrets and RabbitMQ resources
```

The shared base never owns registry identity or environment bootstrap. Each overlay owns
its digest and runtime-specific integration.

## Delivery, metadata and policy

| Location | Ownership |
| --- | --- |
| `platform/components.yaml` | Canonical service, migration and operator component inventory |
| `platform/toolchain.yaml` | Reviewed tool versions and checksum contract |
| `platform/operator-config.example.yaml` | Documented local config shape for `platformctl` |
| `.github/actions/` | Reusable build, scan, copy, component detection and GitOps PR steps |
| `.github/workflows/` | Public CI/release/environment intent; domain logic stays in `platformctl` |
| `policy/config/` | Environment configuration decisions |
| `policy/kubernetes/` | Rendered PROD Kubernetes semantic rules |
| `policy/terraform/` | DEV/CI/PROD reconcile and teardown plan policies |
| `policy/workflows/` | Workflow security constraints |
| `scripts/autoscaling/` | DEV HPA load and behavior verification |
| `scripts/rehearsal/` | Guard failover, removal and upgrade/rollback scenarios |
| `scripts/ci/` | Deterministic CI leaf operations |

## PlatformOwnershipGuard module

```text
platform-ownership-guard/
├── api/v1alpha1/                OwnershipAudit API and generated CRD types
├── cmd/                         controller-runtime manager entrypoint
├── internal/
│   ├── controller/              Reconcile, status and finding transitions
│   ├── inventory/               Kubernetes and Argo evidence normalization
│   ├── detectors/               Pure ArgoPruneRisk and StaleOwnerReference evaluation
│   ├── telemetry/               Bounded metrics
│   └── security/                Read-only RBAC contract tests
├── config/
│   ├── crd, rbac, manager       Installation and runtime resources
│   ├── dev, prod                Environment overlays
│   └── observability            ServiceMonitor, alerts and Grafana dashboard
└── test/                        API-server and deployment-level test assets
```

The operator is an independent Go module and consumes Kubernetes/Argo ownership signals.
It does not become the owner of Terraform, delivery policy or external operator logic.

## Application/platform boundary

The original application remains under `cmd/<service>`, `internal/<service>`, `pkg`,
`proto`, `db` and the local Docker Compose path. CoffeeShop Platform begins at the
infrastructure, automation, policy, GitOps and operator layers described above. Keeping
this boundary explicit avoids presenting upstream application implementation as
work introduced by this repository.
