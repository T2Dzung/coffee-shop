# Kubernetes source-of-truth layout

This directory separates portable workloads from environment-owned platform resources.

```text
apps/
└── coffeeshop/
    ├── base/          Portable CoffeeShop Deployments and Services
    └── overlays/
        ├── dev/       DEV image digests, HPA, config and K3s-specific resources
        └── prod/      PROD image digests, jobs, config and ALB Ingress

ci/
└── arc/               Trusted-build runner controller and runner image

environments/
├── dev/
│   ├── bootstrap/     Add-on and runtime Argo CD root Applications
│   ├── gitops/
│   │   ├── applications/          Cluster add-on Applications
│   │   ├── runtime-applications/  CoffeeShop and ownership-guard Applications
│   │   ├── addons/                Helm values and platform resources
│   │   └── apps/                  CloudNativePG and RabbitMQ application charts
│   ├── network/       Cilium Gateway API resources
│   └── policies/      Default-deny and workload allow policies
└── prod/
    ├── bootstrap/     Bounded AppProject and three PROD Applications
    └── platform/      Storage, External Secrets, RabbitMQ and controller values
```

## Ownership rules

- In normal operation, the repository default branch is the Git source of truth for both
  environments. Git-backed Argo CD sources track `HEAD`; third-party Helm sources pin
  chart versions. PROD also accepts an explicit revision override for controlled
  recovery or testing.
- PR validation never publishes a release artifact. After merge, the trusted candidate
  workflow builds a source revision once, pins DEV by digest and permits PROD promotion
  only when matching QA evidence exists.
- `apps/coffeeshop/base` cannot contain an AWS account ID, an environment registry or
  cluster bootstrap resources.
- Environment-owned image identity belongs only in `overlays/dev` or `overlays/prod`.
- DEV uses separate root Applications for cluster add-ons and runtime workloads. Data
  dependencies become ready before the runtime root Application is created.
- PROD separates platform dependencies, application workloads and ownership auditing
  into bounded Argo CD Applications. It never recurses into DEV source.
- Generated dependants belong to their controller: for example, RabbitMQ Operator owns
  its StatefulSet and External Secrets Operator owns generated Kubernetes Secrets.
- Temporary probes, test manifests and superseded phase resources do not belong in the
  active tree. Historical evidence stays in phase documentation and Git history.
- The typed validation entrypoint enforces the layout contract through
  `bash scripts/validate-infra.sh` with the relevant Kubernetes profile.
