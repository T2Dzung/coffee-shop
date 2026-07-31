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
| [PROD RDS PITR drill](runbooks/prod-rds-pitr-drill.md) | Isolated point-in-time restore, validation, resume and exact cleanup |

## Diagram sources

Rendered diagrams and screenshots are stored in [`img/`](img/). These three versioned
Excalidraw files are the current editable sources of truth:

| Published diagram | Editable source | Scope |
| --- | --- | --- |
| [`dev-architecture.svg`](img/dev-architecture.svg) | [`dev-architecture-v2.excalidraw`](diagrams/dev-architecture-v2.excalidraw) | VPC and subnet boundary, HAProxy, K3s, service protocols, state, operators and observability |
| [`prod-architecture.svg`](img/prod-architecture.svg) | [`prod-architecture-v2.excalidraw`](diagrams/prod-architecture-v2.excalidraw) | ALB/EKS, controllers, service protocols, RabbitMQ/EBS, RDS, secrets and CloudWatch |
| [`delivery-flow.svg`](img/delivery-flow.svg) | [`delivery-flow-v2.excalidraw`](diagrams/delivery-flow-v2.excalidraw) | Protected source, immutable digest, DEV, manual QA, PROD, emergency and rollback lanes |

Edit the mapped Excalidraw source and export the published SVG; do not make a diagram
change only in the generated SVG. See the
[diagram workflow](diagrams/README.md).

- `coffeeshop`: the upstream event-driven application flow.
- `coffeeshop_hashicorp`: the retained upstream deployment-diagram reference; it does
  not describe the current platform.
- `clean_ddd`: the upstream application design diagram.
