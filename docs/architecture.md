# CoffeeShop Platform architecture

CoffeeShop Platform runs the same event-driven application in two intentionally
different environments. Git, immutable image digests, Kustomize and Argo CD are shared;
the runtime infrastructure is not.

## DEV: self-managed Kubernetes

![CoffeeShop DEV architecture](img/dev-architecture.svg)

### Request and service path

1. The Terraform output `active_api_endpoint` resolves to the public Elastic IP on the
   HAProxy node.
2. HAProxy forwards traffic to the Cilium-managed Envoy Gateway in the K3s cluster.
3. Gateway API routes `/` to `web:8888` and rewrites `/api` to `/` before sending it to
   `proxy:5000`.
4. `proxy` calls `product:5001` for the catalog and `counter:5002` for orders.
5. `counter` persists order state in CloudNativePG and publishes work to RabbitMQ.
6. The barista and kitchen workers consume their queues, persist their results and emit
   fulfillment events back to `counter`.

The cluster has three K3s control-plane nodes with embedded etcd. Cilium replaces
`kube-proxy`, provides the eBPF data plane, implements Gateway API and enforces the
default-deny plus workload allow policies. HPA owns replica count for `proxy` and
`product`; their Deployment overlays intentionally remove `spec.replicas`.

### Stateful and observability path

- CloudNativePG operates a three-instance PostgreSQL cluster. RabbitMQ Cluster Operator
  operates a three-replica quorum cluster. Their Longhorn storage classes use one volume
  replica because database and queue redundancy is provided at the application layer.
- Application spans go through OpenTelemetry Collector to Tempo. Grafana Alloy reads
  Pod logs and sends them to Loki. Prometheus scrapes metrics and Alertmanager evaluates
  alerts. Grafana reads Prometheus, Loki and Tempo for correlated investigation.
- Argo CD owns the add-on and runtime Applications. The data Applications become ready
  before the runtime App-of-Apps is created.
- PlatformOwnershipGuard reads Kubernetes ownership metadata and emits findings; it has
  no permission to mutate audited workloads.
- cert-manager is installed as a DEV add-on, but the current source contains no public
  `Certificate` or HTTPS Gateway listener. The exposed application route is HTTP.

## PROD: AWS-managed boundaries

![CoffeeShop PROD architecture](img/prod-architecture.svg)

### Request and service path

1. AWS Load Balancer Controller reconciles the Kubernetes Ingress into an
   internet-facing ALB.
2. The ALB uses IP targets: `/` goes directly to ready `web` Pod IPs and `/api` goes to
   ready `proxy` Pod IPs in private EKS subnets.
3. The application call graph is the same as DEV, but `counter`, `barista` and `kitchen`
   persist to private encrypted RDS PostgreSQL instead of an in-cluster database.
4. RabbitMQ remains inside EKS. RabbitMQ Cluster Operator renders a three-replica
   StatefulSet, one encrypted gp3 EBS volume per replica, a disruption budget and a
   network policy.

The current ALB listener is HTTP port 80. No public custom domain, ACM certificate or
HTTPS listener is declared in the active PROD Ingress.

### Secrets, schema and operations

- EKS Pod Identity gives AWS Load Balancer Controller, External Secrets Operator, EBS
  CSI and CloudWatch agent separate AWS identities.
- External Secrets Operator reads only the allowed Secrets Manager records and owns the
  generated Kubernetes Secrets. Application Pods never read Secrets Manager directly.
- Argo CD runs a bootstrap Job with the RDS master credential to create or rotate the
  shared non-master `coffeeshop_app` role. The migration Job and the three stateful
  services use that role; it is not a per-service database security boundary.
- CloudWatch Container Insights, logs and alarms are the PROD observability boundary.
  Terraform defines a lifecycle-bound Synthetics canary for the public read-only
  `item-types` journey and links its alarm to the
  [golden-journey runbook](runbooks/prod-golden-journey.md). The current environment
  enables it while PROD is online; `prod teardown` removes its recurring runtime and the
  next `prod setup` recreates it. The public tfvars example remains opt-in to avoid
  surprising a new user with recurring cost. The DEV Prometheus/Grafana/Loki/Tempo stack
  is not copied into PROD.
- Three Argo CD Applications independently own the platform dependencies, CoffeeShop
  workload overlay and PlatformOwnershipGuard.
- `platformctl prod restore-drill` exercises the configured RDS automated-backup path.
  It restores an exact timestamp into a separate private Single-AZ target, validates
  before/after markers plus a bounded application schema, and requires a second approval
  to remove that exact target. It does not change the application Secret, endpoint or
  traffic path. See the [PITR drill runbook](runbooks/prod-rds-pitr-drill.md).

RDS is private, encrypted and Single-AZ in the current cost-bounded profile. EKS nodes
span two private subnets/AZs; the three RabbitMQ Pods are spread across the available
worker nodes but do not prove three-AZ resilience. The isolated PITR drill proves one
bounded database recovery path, not Multi-AZ failover or full disaster recovery.

## Runtime and administrative endpoints

Endpoints are runtime outputs, not source constants.

| Surface | How to resolve or open it | Exposure |
| --- | --- | --- |
| DEV application | `DEV_HOST=$(terraform -chdir=infrastructure/terraform/envs/dev output -raw active_api_endpoint)` then `http://$DEV_HOST/` | Public HTTP through EIP and HAProxy |
| DEV API | `http://$DEV_HOST/api` | Public HTTP through the same Gateway |
| PROD application | `PROD_HOST=$(kubectl get ingress coffeeshop-prod-alb-ingress -n coffeeshop -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')` then `http://$PROD_HOST/` | Public HTTP through ALB |
| PROD API | `http://$PROD_HOST/api` | Public HTTP through the same ALB |
| Argo CD | `kubectl port-forward -n argocd svc/argocd-server 8080:443` then `https://localhost:8080` | Local operator access only |
| DEV Grafana | `kubectl port-forward -n monitoring svc/kube-prometheus-stack-grafana 3000:80` then `http://localhost:3000` | Local operator access only |
| DEV Prometheus | `kubectl port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090:9090` | Local operator access only |
| DEV Alertmanager | `kubectl port-forward -n monitoring svc/kube-prometheus-stack-alertmanager 9093:9093` | Local operator access only |
| PROD telemetry | AWS CloudWatch console for the selected account and region | AWS-authenticated access |

Choose the environment kubeconfig before cluster-local commands. Loki and Tempo are
queried through Grafana data sources rather than exposed as public services.

## Delivery from source to PROD

![CoffeeShop Platform delivery workflow](img/delivery-flow.svg)

| Stage | Owner and trigger | Execution boundary | Merge behavior |
| --- | --- | --- | --- |
| Feature PR | Developer opens a PR; `ci-app.yml` and `ci-platform.yml` run | GitHub-hosted; catalog-scoped unit tests run without exposing a persistent self-hosted runner to untrusted source | Human review and protected-branch merge |
| Candidate build | `release-candidate.yml` runs after the protected source merge | GitHub-hosted component detection and ECR preflight; self-hosted ARC `trusted-build` runs the shared component test gate before build, scan, SBOM and signing | Candidate evidence PR is submitted from GitHub-hosted capacity and auto-merges only after required checks |
| DEV delivery | `dev-deliver.yml` copies the exact candidate into DEV ECR and edits the DEV Kustomize overlay | GitHub-hosted with environment-scoped OIDC | One desired-state PR auto-merges after required checks |
| DEV reconcile | DEV Argo CD tracks `HEAD`, renders the overlay and self-heals the exact digest | In-cluster Argo CD; not a GitHub runner job | Automatic GitOps reconciliation; runtime health is checked separately |
| QA | An operator runs `release-qa.yml` for an exact source and release set | Human dispatch; GitHub-hosted validation and evidence write | Manual approve/reject decision; evidence PR auto-merges after checks |
| PROD promotion | `prod-promote.yml` re-verifies, re-scans and copies the reviewed digest, then edits the PROD overlay | GitHub-hosted with environment-scoped OIDC | Standard release sets and manually dispatched stateful maintenance auto-merge after required checks |
| PROD reconcile | PROD Argo CD applies the platform dependencies and application overlay | In-cluster Argo CD; not a GitHub runner job | Automatic GitOps reconciliation |

The GitHub Environment objects are identity and secret boundaries, not a substitute for
the manual QA decision. Emergency and rollback workflows are separately dispatched,
bounded paths; they do not redefine the standard promotion contract. Emergency tests
the exact patch with the same catalog-owned action before artifact creation. Rollback
does not rebuild or rerun source tests: it verifies and selects a previously reviewed,
immutable PROD digest.

The CI execution plane is managed independently with `platformctl ci
setup/status/teardown`. Automatic post-merge candidate builds require the
`trusted-build` ARC label to be configured and reachable. Manual dispatch can use the
documented GitHub-hosted fallback after an explicit routing decision; DEV delivery, QA,
PROD promotion, emergency and rollback do not depend on the CI K3s runtime.

## Ownership rules

- Shared application manifests stay environment-neutral.
- Environment overlays own registry, digest and environment-specific resources.
- Terraform owns cloud resources and remote state; Ansible owns DEV host and bootstrap
  sequencing.
- Argo CD owns declared Kubernetes desired state. External operators own the dependent
  resources they generate from their custom resources.
- PlatformOwnershipGuard observes ownership signals but does not mutate targets.
