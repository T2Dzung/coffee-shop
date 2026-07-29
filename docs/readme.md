# CoffeeShop Platform documentation

This directory is the public documentation entrypoint for the platform. It keeps the
root README concise while separating application provenance, architecture and code
ownership.

## Start here

| Document | Purpose |
| --- | --- |
| [Architecture](architecture.md) | Detailed DEV, PROD, runtime-access and delivery flows |
| [Platform layout](platform-layout.md) | Detailed infrastructure folders, Go packages and ownership boundaries |
| [Application](application.md) | Upstream service flow, local development and screenshots |
| [Kubernetes layout](../infrastructure/k8s/README.md) | GitOps source-of-truth and environment ownership |
| [PlatformOwnershipGuard](../platform-ownership-guard/README.md) | Read-only Kubernetes ownership auditing |
| [PROD golden-journey runbook](runbooks/prod-golden-journey.md) | Synthetic alert diagnosis, source-managed recovery and bounded cleanup |

## Diagram sources

Rendered diagrams and screenshots are stored in [`img/`](img/). The matching
`.excalidraw` files in [`diagrams/`](diagrams/) are the source of truth for the three
platform diagrams. Edit only the Excalidraw source, then run
`python3 scripts/docs/render-excalidraw-svg.py` from the repository root. The generated
SVG files must not be edited by hand. See the [diagram workflow](diagrams/README.md).

- `dev-architecture`: request, service, data, operator and observability paths on K3s.
- `prod-architecture`: ALB/EKS, RDS, EBS, secrets, operators and CloudWatch boundaries.
- `delivery-flow`: PR checks, trusted build, GitOps delivery, manual QA and PROD promotion.
- `coffeeshop`: the upstream event-driven application flow.
- `coffeeshop_hashicorp`: the retained upstream deployment-diagram reference; it does
  not describe the current platform.
- `clean_ddd`: the upstream application design diagram.
