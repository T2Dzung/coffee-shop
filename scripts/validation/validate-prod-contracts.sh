#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BOOTSTRAP_TF_DIR="${PROJECT_ROOT}/infrastructure/terraform/bootstrap/prod"
PROD_TF_DIR="${PROJECT_ROOT}/infrastructure/terraform/envs/prod"
MODULES_DIR="${PROJECT_ROOT}/infrastructure/terraform/modules"

fail() {
  echo "PROD validation failed: $*" >&2
  exit 1
}

[[ -f "${PROD_TF_DIR}/terraform.tfvars.example" ]] || \
  fail "clone-ready terraform.tfvars.example is missing"
[[ -f "${PROJECT_ROOT}/scripts/estimate-prod-hourly-cost.sh" ]] || \
  fail "dynamic PROD hourly estimator is missing"
grep -Eq 'productFamily,Value=Load Balancer-(Network|Application)' \
  "${PROJECT_ROOT}/scripts/estimate-prod-hourly-cost.sh" || \
  fail "PROD hourly estimator must include the fixed load balancer price"
grep -Fq '^(setup|g4|reconcile|teardown)$' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "setup/G4 must select the load balancer-inclusive hourly estimate"
grep -Fq 'public_ipv4_count=3' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G4 estimate must include the NAT and two internet-facing load balancer public IPv4 addresses"
grep -Fq 'Usage: scripts/deploy-prod.sh setup|teardown|' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "PROD operator interface must expose setup and teardown as the primary actions"

# Generic PROD sources must be reusable by a caller selecting any valid account.
# Official third-party owner IDs belong in provider data sources, but operator account
# IDs do not belong in this PROD/bootstrap surface.
mapfile -d '' generic_sources < <(
  find "${PROD_TF_DIR}" "${BOOTSTRAP_TF_DIR}" \
    -path '*/.terraform' -prune -o -type f -name '*.tf' -print0
  printf '%s\0' "${PROJECT_ROOT}/scripts/deploy-prod.sh"
)
if grep -En '(^|[^0-9])[0-9]{12}([^0-9]|$)' "${generic_sources[@]}"; then
  fail "generic PROD/bootstrap sources contain a literal 12-digit AWS account ID"
fi

# Partial backend configuration must not own account-specific coordinates in source.
for root in "${PROD_TF_DIR}" "${BOOTSTRAP_TF_DIR}"; do
  if grep -Eq '^[[:space:]]*(bucket|key|region|kms_key_id|role_arn)[[:space:]]*=' \
    "${root}/backend.tf"; then
    fail "${root}/backend.tf must receive backend coordinates through -backend-config"
  fi
  grep -Eq 'backend[[:space:]]+"s3"[[:space:]]*\{' "${root}/backend.tf" || \
    fail "${root}/backend.tf must declare a partial S3 backend"
done

if grep -Eq 'dynamodb_table|G5.*(PASSED|Complete)' "${PROJECT_ROOT}/scripts/deploy-prod.sh"; then
  fail "Orchestrator must use native S3 locking and must not claim G5"
fi

grep -Fq 'backend-config=kms_key_id=' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "PROD S3 backend must write state with the account-local KMS key"

if grep -Fq 'depends_on = [module.eks_cluster]' "${PROD_TF_DIR}/eks.tf"; then
  fail "managed nodes must not depend on the whole EKS module because CoreDNS requires nodes"
fi
grep -Fq 'reconcile_coredns_after_nodes' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G3 must reconcile CoreDNS only after managed nodes are Ready"
grep -Fq 'terraform_coredns_is_tainted' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G3 must guard interrupted CoreDNS create recovery"

# Fail-closed selection is the invariant; the chosen account and Region stay inputs.
grep -Fq 'PROD_EXPECTED_AWS_ACCOUNT_ID' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "deploy-prod.sh must require an explicit expected account"
grep -Fq 'PROD_EXPECTED_AWS_REGION' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "deploy-prod.sh must require an explicit expected Region"
for root in "${PROD_TF_DIR}" "${BOOTSTRAP_TF_DIR}"; do
  grep -Eq 'account_id[[:space:]]*==[[:space:]]*var\.expected_aws_account_id' "${root}/main.tf" || \
    fail "${root}/main.tf must compare the caller with expected_aws_account_id"
done

for module_dir in terraform-backend vpc eks-cluster eks-managed-node; do
  mod_path="${MODULES_DIR}/${module_dir}"
  [[ -d "${mod_path}" ]] || fail "required module ${module_dir} is missing"
  mapfile -t tf_files < <(find "${mod_path}" -maxdepth 1 -name '*.tf' -exec basename {} \; | sort)
  [[ "${tf_files[*]}" == "main.tf outputs.tf variables.tf" ]] || \
    fail "module ${module_dir} must contain only main.tf, outputs.tf and variables.tf; found: ${tf_files[*]}"
done

NODE_MODULE="${MODULES_DIR}/eks-managed-node/main.tf"
for contract in \
  'volume_type[[:space:]]*=[[:space:]]*var\.volume_type' \
  'iops[[:space:]]*=[[:space:]]*var\.volume_iops' \
  'throughput[[:space:]]*=[[:space:]]*var\.volume_throughput' \
  'delete_on_termination[[:space:]]*=[[:space:]]*true'; do
  grep -Eq "${contract}" "${NODE_MODULE}" || fail "managed-node gp3 lifecycle contract is incomplete"
done
grep -Eq 'version[[:space:]]*=[[:space:]]*aws_launch_template\.node\.latest_version' \
  "${NODE_MODULE}" || \
  fail "managed node group must pin the resolved launch-template version so full plans converge"

grep -Eq 'vpc_endpoint_type[[:space:]]*=[[:space:]]*"Gateway"' "${MODULES_DIR}/vpc/main.tf" || \
  fail "VPC module must include an S3 Gateway Endpoint"

grep -Eq 'include_credit[[:space:]]*=[[:space:]]*false' "${PROD_TF_DIR}/budget.tf" || \
  fail "Budget must track gross usage by excluding credits"
grep -Eq 'limit_amount[[:space:]]*=[[:space:]]*var\.budget_limit_amount' "${PROD_TF_DIR}/budget.tf" || \
  fail "Budget limit must remain configurable"
grep -Fq 'dynamic "notification"' "${PROD_TF_DIR}/budget.tf" || \
  fail "optional Budget notification must be omitted when no subscriber is configured"

grep -Eq 'public_access_cidrs[[:space:]]*=[[:space:]]*var\.endpoint_public_access_cidrs' \
  "${MODULES_DIR}/eks-cluster/main.tf" || fail "EKS public API CIDRs must be configurable"
grep -Fq 'cidr != "0.0.0.0/0"' "${PROD_TF_DIR}/variables.tf" || \
  fail "PROD must reject an unrestricted public EKS API CIDR"

grep -Fq 'Resource = [for repository in aws_ecr_repository.app : repository.arn]' \
  "${PROD_TF_DIR}/iam.tf" || fail "delivery ECR actions must be repository-scoped"

for repository in web proxy product counter barista kitchen; do
  grep -Eq "go-coffeeshop-${repository}" "${PROD_TF_DIR}/ecr.tf" || \
    fail "CoffeeShop ECR contract is missing ${repository}"
done
grep -Fq 'platform-ownership-guard' "${PROD_TF_DIR}/ecr.tf" || \
  fail "ECR contract is missing platform-ownership-guard"

LBC_POLICY="${PROD_TF_DIR}/policies/aws-load-balancer-controller-v3.4.2.json"
EXPECTED_LBC_POLICY_SHA256="6203c2b12b9cfad35b441b1d4d9cb70b50153fa2e402ac58cf636c7311480a56"
ACTUAL_LBC_POLICY_SHA256="$(jq -cS . "${LBC_POLICY}" | sha256sum | awk '{print $1}')"
[[ "${ACTUAL_LBC_POLICY_SHA256}" == "${EXPECTED_LBC_POLICY_SHA256}" ]] || \
  fail "vendored AWS Load Balancer Controller policy hash does not match the reviewed artifact"

LBC_VALUES="${PROJECT_ROOT}/infrastructure/k8s/environments/prod/platform/aws-load-balancer-controller-values.yaml"
[[ -f "${LBC_VALUES}" ]] || \
  fail "AWS Load Balancer Controller Helm values are missing"

grep -Fq 'AWS_LB_CONTROLLER_CHART_VERSION="3.4.2"' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G4 must pin the reviewed AWS Load Balancer Controller chart"
grep -Fq -- "--version \"\${AWS_LB_CONTROLLER_CHART_VERSION}\"" \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G4 Helm installation must use the reviewed chart pin"
grep -Eq 'vpc_id="\$\(terraform_run "\$\{PROD_TF_DIR\}" output -raw vpc_id\)"' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G4 must discover the account-local VPC ID from Terraform state"
grep -Eq -- '--set-string vpcId="\$\{vpc_id\}"' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G4 must pass VPC ID explicitly instead of depending on Pod access to EC2 metadata"
grep -Fq -- '--atomic' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G4 Helm deployment must roll back automatically when readiness fails"
grep -Fq 'describe-pod-identity-association' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G4 must verify the exact Pod Identity role association"
grep -Fq 'expected exactly one IP target group' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G4 target group discovery must fail closed"
grep -Fq 'healthy ALB targets' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "G4 must verify healthy IP targets across Availability Zones"

grep -Fq 'output "aws_load_balancer_controller_role_arn"' "${PROD_TF_DIR}/outputs.tf" || \
  fail "PROD outputs must expose aws_load_balancer_controller_role_arn"

grep -Fq 'run_g4' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "deploy-prod.sh must implement run_g4 action"

grep -Fq 'run_reconcile' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "WP4 must implement a non-targeted full reconcile action"
grep -Fq 'full reconcile plan contains delete/replacement actions' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "WP4 reconcile must reject delete and replacement actions"
grep -Fq 'plan -input=false -detailed-exitcode' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "WP4 reconcile must require an empty follow-up plan"
grep -Fq 'run_teardown' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "WP4 must implement an ordered teardown action"
grep -Fq 'PROD_CONFIRM_TEARDOWN' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "WP4 teardown must require an account-scoped confirmation"
grep -Fq 'deleting its ALB Ingress directly while the controller is healthy' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "WP4 teardown must delete controller-owned ingress before Terraform resources"
teardown_confirm_line="$(grep -n 'confirm_saved_plan "teardown"' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" | head -n 1 | cut -d: -f1)"
ingress_delete_line="$(grep -n 'kubectl delete ingress coffeeshop-prod-alb-ingress' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" | head -n 1 | cut -d: -f1)"
[[ -n "${teardown_confirm_line}" && -n "${ingress_delete_line}" &&
  "${teardown_confirm_line}" -lt "${ingress_delete_line}" ]] || \
  fail "WP4 teardown must approve the saved destroy plan before its first live deletion"
grep -Fq 'retained ECR/Budget resources' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "WP4 teardown plan must reject retained-resource deletes"
grep -Fq 'verify_teardown_inventory' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "WP4 teardown must inventory billable orphans and retained resources"

# PROD-2 GitOps and ALB contracts
PROD_2_BOOTSTRAP="${PROJECT_ROOT}/infrastructure/k8s/environments/prod/bootstrap"
PROD_2_OVERLAY="${PROJECT_ROOT}/infrastructure/k8s/apps/coffeeshop/overlays/prod"

[[ -f "${PROD_2_BOOTSTRAP}/appproject.yaml" ]] || \
  fail "PROD-2 AppProject manifest is missing"
[[ -f "${PROD_2_BOOTSTRAP}/coffeeshop-prod-app.yaml" ]] || \
  fail "PROD-2 Argo Application manifest is missing"
[[ -f "${PROD_2_OVERLAY}/ingress.yaml" ]] || \
  fail "PROD-2 ALB Ingress manifest is missing"
[[ -f "${PROJECT_ROOT}/.github/workflows/promote-prod.yml" ]] || \
  fail "PROD-2 promotion workflow is missing"
[[ -f "${PROJECT_ROOT}/.github/workflows/commission-prod.yml" ]] || \
  fail "PROD-2 commissioning workflow is missing"

awk '
  /^spec:/ { in_spec=1; next }
  in_spec && /^[^[:space:]]/ { in_spec=0 }
  in_spec && /^[[:space:]]+ingressClassName:[[:space:]]+alb[[:space:]]*$/ { found=1 }
  END { exit(found ? 0 : 1) }
' "${PROD_2_OVERLAY}/ingress.yaml" || \
  fail "PROD-2 Ingress must set spec.ingressClassName to alb"
grep -Fq 'alb.ingress.kubernetes.io/target-type: ip' "${PROD_2_OVERLAY}/ingress.yaml" || \
  fail "PROD-2 ALB Ingress must specify IP target type"
grep -Fq 'alb.ingress.kubernetes.io/scheme: internet-facing' "${PROD_2_OVERLAY}/ingress.yaml" || \
  fail "PROD-2 ALB Ingress must specify internet-facing scheme"

grep -Fq 'environment: prod' "${PROJECT_ROOT}/.github/workflows/promote-prod.yml" || \
  fail "PROD-2 promotion workflow must require prod environment approval"
grep -Fq 'environment: prod' "${PROJECT_ROOT}/.github/workflows/commission-prod.yml" || \
  fail "PROD-2 commissioning workflow must require prod environment approval"
grep -Fq 'release_mode: "commissioning"' \
  "${PROJECT_ROOT}/.github/workflows/commission-prod.yml" || \
  fail "PROD-2 commissioning workflow must record its bypass boundary explicitly"
if grep -Eq '(DEV_ARTIFACT_READ_ROLE_ARN|environment:[[:space:]]+dev)' \
  "${PROJECT_ROOT}/.github/workflows/commission-prod.yml"; then
  fail "PROD-2 commissioning workflow must not require DEV."
fi
grep -Fq 'id-token: write' "${PROJECT_ROOT}/.github/workflows/promote-prod.yml" || \
  fail "PROD-2 promotion workflow must request id-token write permission for OIDC"
grep -Fq "exit-code: '1'" "${PROJECT_ROOT}/.github/workflows/promote-prod.yml" || \
  fail "PROD-2 vulnerability gate must fail closed"
grep -Eq 'uses:[[:space:]]+[^[:space:]]+@[0-9a-f]{40}([[:space:]]|$)' \
  "${PROJECT_ROOT}/.github/workflows/promote-prod.yml" || \
  fail "PROD-2 workflow actions must use immutable commit pins"
if grep -Eq 'uses:[[:space:]]+[^[:space:]]+@(master|main|v[0-9]+)([[:space:]]|$)' \
  "${PROJECT_ROOT}/.github/workflows/promote-prod.yml"; then
  fail "PROD-2 workflow contains a mutable action reference"
fi
grep -Fq 'pull-requests: write' "${PROJECT_ROOT}/.github/workflows/promote-prod.yml" || \
  fail "PROD-2 workflow must request permission to submit its desired-state PR"
grep -Fq 'scripts/ci/create-gitops-pr.sh' "${PROJECT_ROOT}/.github/workflows/promote-prod.yml" || \
  fail "PROD-2 workflow must submit desired state through protected-main review"
for contract in \
  'qa_status == "approved"' \
  'DEV_ARTIFACT_READ_ROLE_ARN' \
  'crane copy' \
  'DESTINATION_DIGEST' \
  'SOURCE_DIGEST'; do
  grep -Fq "${contract}" "${PROJECT_ROOT}/.github/workflows/promote-prod.yml" || \
    fail "PROD-2 zero-rebuild workflow is missing contract: ${contract}"
done
if grep -Eq '(bash scripts/ci/build-go-service.sh|docker build|docker/build-push-action@)' \
  "${PROJECT_ROOT}/.github/workflows/promote-prod.yml"; then
  fail "PROD-2 promotion must not rebuild the QA-approved application artifact"
fi
grep -Fq 'statefulset/argocd-application-controller' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "Argo CD application-controller rollout must use its StatefulSet workload kind"
grep -Fq 'did not become Synced and Healthy in time' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "Argo CD polling must fail closed on timeout"
grep -Fq 'healthy ALB target IPs' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "ALB runtime verification must compare healthy targets with Ready Pod IPs"
grep -Fq 'CoffeeShop POS' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "ALB runtime verification must require the expected application marker"
grep -Fq 'verify-gitops-health.sh" coffeeshop-prod' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "reconcile must run the standard GitOps health verifier"
grep -Fq 'verify-gitops-health.sh" coffeeshop-prod-platform' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "reconcile must verify the platform Argo CD application too"
grep -Fq -- '--api-group=external-secrets.io' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "teardown must detect whether the optional PROD-3 ExternalSecret API exists"
grep -Fq 'externalsecrets.external-secrets.io' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "teardown must address ExternalSecret through its fully qualified resource name"
grep -Fq -- '--resource-type-filters elasticloadbalancing:loadbalancer' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "teardown resume must recover a controller-owned ALB by its PROD tags"
grep -Fq 'resuming the wait for their tagged ALB deletion' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "teardown must resume safely after Application and Ingress deletion"
grep -Fq 'kubectl delete namespace rabbitmq-system' \
  "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "teardown must remove the optional RabbitMQ operator without replaying unavailable CRD kinds"
if grep -Fq 'kubectl delete --ignore-not-found' "${PROJECT_ROOT}/scripts/deploy-prod.sh"; then
  fail "teardown must not replay the RabbitMQ operator manifest during deletion"
fi
if grep -Fq 'Key=Phase,Values=PROD-1' "${PROJECT_ROOT}/scripts/deploy-prod.sh"; then
  fail "teardown inventory must cover all PROD phases"
fi
for obsolete_tree in bootstrap gateway policies gitops prod legacy; do
  [[ ! -d "${PROJECT_ROOT}/infrastructure/k8s/${obsolete_tree}" ]] || \
    fail "obsolete Kubernetes tree remains active: infrastructure/k8s/${obsolete_tree}"
done
grep -Fq '__GITOPS_REPO_URL__' "${PROD_2_BOOTSTRAP}/appproject.yaml" || \
  fail "AppProject repository must remain clone-configurable"
grep -Fq '__GITOPS_REVISION__' "${PROD_2_BOOTSTRAP}/coffeeshop-prod-app.yaml" || \
  fail "Argo CD target revision must remain clone-configurable"

# PROD-3 managed integration and full release-set contracts.
PROD_PLATFORM="${PROJECT_ROOT}/infrastructure/k8s/environments/prod/platform"
PROD_BUILD_SCRIPT="${PROJECT_ROOT}/scripts/ci/build-go-service.sh"
PROD_SECRETS_TF="${PROD_TF_DIR}/secrets.tf"

[[ -f "${PROJECT_ROOT}/cmd/migrate/main.go" &&
   -f "${PROJECT_ROOT}/docker/Dockerfile-migrate" ]] || \
  fail "PROD-3 requires a dedicated migration command and image"
if grep -Fq -- '-tags migrate' "${PROD_BUILD_SCRIPT}"; then
  fail "normal CoffeeShop service builds must not run migrations from init()"
fi
grep -Fq 'MIGRATION_MODE must be bootstrap or migrate' \
  "${PROJECT_ROOT}/cmd/migrate/main.go" || \
  fail "migration image must expose bounded bootstrap and migrate modes"

if grep -Eq 'random_password|aws_secretsmanager_secret_version|secret_string' "${PROD_SECRETS_TF}"; then
  fail "application credential material must not enter Terraform state"
fi
[[ -f "${PROJECT_ROOT}/scripts/seed-prod-secrets.sh" ]] || \
  fail "runtime application secret seed helper is missing"
grep -Fq 'put-secret-value' "${PROJECT_ROOT}/scripts/seed-prod-secrets.sh" || \
  fail "secret seed helper must write through Secrets Manager API"

for service in web proxy product counter barista kitchen migrate; do
  grep -Fq "name: go-coffeeshop-${service}" "${PROD_2_OVERLAY}/kustomization.yaml" || \
    fail "PROD release set is missing ${service}"
  grep -Fq -- "- ${service}" "${PROJECT_ROOT}/.github/workflows/commission-prod.yml" || \
    fail "commission workflow cannot build ${service}"
done
for workflow in commission-prod.yml promote-prod.yml; do
  grep -Fq 'scripts/ci/update-kustomize-image.sh' \
    "${PROJECT_ROOT}/.github/workflows/${workflow}" || \
    fail "${workflow} must update exactly one structured image entry"
  if grep -Eq 'sed -i .*newName:|sed -i .*digest:' \
    "${PROJECT_ROOT}/.github/workflows/${workflow}"; then
    fail "${workflow} contains a release-set-wide sed replacement"
  fi
done
FULL_STACK_WORKFLOW="${PROJECT_ROOT}/.github/workflows/commission-prod-stack.yml"
[[ -f "${FULL_STACK_WORKFLOW}" ]] || \
  fail "one-run PROD full-stack commissioning workflow is missing"
[[ "$(grep -c 'uses: aquasecurity/trivy-action@' "${FULL_STACK_WORKFLOW}")" -eq 7 ]] || \
  fail "full-stack commissioning must scan all seven exact local images"
grep -Fq 'infrastructure/releases/prod/release-sets/' "${FULL_STACK_WORKFLOW}" || \
  fail "full-stack commissioning must record one immutable release manifest"
for workflow in commission-prod.yml commission-prod-stack.yml promote-prod.yml; do
  grep -Fq 'group: prod-desired-state' "${PROJECT_ROOT}/.github/workflows/${workflow}" || \
    fail "${workflow} must serialize PROD desired-state mutation"
done

[[ -f "${PROD_2_BOOTSTRAP}/coffeeshop-prod-platform-app.yaml" ]] || \
  fail "PROD platform Argo CD Application is missing"
grep -Fq 'path: infrastructure/k8s/environments/prod/platform' \
  "${PROD_2_BOOTSTRAP}/coffeeshop-prod-platform-app.yaml" || \
  fail "PROD platform Application points to the wrong source"
for resource in \
  storage/storageclass-gp3.yaml \
  external-secrets/cluster-secret-store.yaml \
  external-secrets/external-secret.yaml \
  external-secrets/rabbitmq-external-secret.yaml \
  rabbitmq/rabbitmq-cluster.yaml \
  rabbitmq/pdb.yaml \
  rabbitmq/networkpolicy.yaml; do
  grep -Fq -- "- ${resource}" "${PROD_PLATFORM}/kustomization.yaml" || \
    fail "PROD platform graph is missing ${resource}"
done
if grep -Fq 'auth:' "${PROD_PLATFORM}/external-secrets/cluster-secret-store.yaml"; then
  fail "ESO EKS Pod Identity store must use the controller credential chain, not auth.jwt"
fi
grep -Fq 'external-secrets.io/v1' \
  "${PROD_PLATFORM}/external-secrets/cluster-secret-store.yaml" || \
  fail "ESO resources must use the pinned controller's served v1 API"
grep -Fq 'monitorAllServices = false' "${PROD_TF_DIR}/eks.tf" || \
  fail "CloudWatch Application Signals auto-monitoring must stay disabled"
grep -Fq '/aws/containerinsights/' "${PROD_TF_DIR}/observability.tf" || \
  fail "CloudWatch retention must own the actual Container Insights log groups"
grep -Fq 'AmazonRDS' "${PROJECT_ROOT}/scripts/estimate-prod-hourly-cost.sh" || \
  fail "hourly estimator must query current RDS pricing"
grep -Fq 'aws_db_instance.postgres' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "PROD teardown must include the RDS data boundary"
grep -Fq 'expected 7:0' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "setup must fail closed until all seven release digests are present"
grep -Fq 'http.StripPrefix("/api"' "${PROJECT_ROOT}/cmd/proxy/main.go" || \
  fail "proxy must strip the public /api prefix before gRPC-Gateway routing"
grep -Fq '/api/v1/api/orders' "${PROJECT_ROOT}/scripts/deploy-prod.sh" || \
  fail "runtime verification must exercise the external full transaction path"

echo "PROD static contracts passed."
