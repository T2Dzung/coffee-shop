#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROD_TF_DIR="${PROJECT_ROOT}/infrastructure/terraform/envs/prod"
BOOTSTRAP_TF_DIR="${PROJECT_ROOT}/infrastructure/terraform/bootstrap/prod"
TFVARS_FILE="${PROD_VAR_FILE:-${PROD_TF_DIR}/terraform.tfvars}"

read_tfvar_string() {
  local key="$1"
  awk -v key="${key}" '
    $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
      line=$0
      sub(/#.*/, "", line)
      sub(/^[^=]*=[[:space:]]*"/, "", line)
      sub(/"[[:space:]]*$/, "", line)
      print line
      exit
    }
  ' "${TFVARS_FILE}"
}

read_tfvar_expression() {
  local key="$1"
  awk -v key="${key}" '
    $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
      line=$0
      sub(/#.*/, "", line)
      sub(/^[^=]*=[[:space:]]*/, "", line)
      sub(/[[:space:]]*$/, "", line)
      print line
      exit
    }
  ' "${TFVARS_FILE}"
}

[[ -f "${TFVARS_FILE}" ]] || {
  echo "Error: PROD variable file not found: ${TFVARS_FILE}" >&2
  echo "Copy terraform.tfvars.example to terraform.tfvars and fill the environment values." >&2
  exit 1
}
ACTION="${1:-}"
EXPECTED_REGION="${PROD_EXPECTED_AWS_REGION:-$(read_tfvar_string aws_region)}"
EXPECTED_ACCOUNT_ID="${PROD_EXPECTED_AWS_ACCOUNT_ID:-$(read_tfvar_string expected_aws_account_id)}"
PROJECT_NAME="${PROD_PROJECT_NAME:-$(read_tfvar_string project_name)}"
ENVIRONMENT="${PROD_ENVIRONMENT:-$(read_tfvar_string environment)}"
ENVIRONMENT="${ENVIRONMENT:-prod}"
CLUSTER_PUBLIC_CIDRS="${TF_VAR_cluster_endpoint_public_access_cidrs:-$(read_tfvar_expression cluster_endpoint_public_access_cidrs)}"
NODE_INSTANCE_TYPES_EXPR="$(read_tfvar_expression node_instance_types)"
NODE_INSTANCE_TYPES_EXPR="${NODE_INSTANCE_TYPES_EXPR:-[\"t3.medium\"]}"
NODE_INSTANCE_TYPE="${PROD_NODE_INSTANCE_TYPE:-$(jq -r '.[0]' <<<"${NODE_INSTANCE_TYPES_EXPR}")}"
NODE_COUNT="${PROD_NODE_COUNT:-$(read_tfvar_expression node_desired_size)}"
NODE_COUNT="${NODE_COUNT:-2}"
NODE_DISK_GIB="${PROD_NODE_DISK_GIB:-$(read_tfvar_expression node_disk_size)}"
NODE_DISK_GIB="${NODE_DISK_GIB:-20}"
STATE_BUCKET_NAME="${PROD_STATE_BUCKET_NAME:-${PROJECT_NAME}-terraform-state-${EXPECTED_ACCOUNT_ID}}"
BOOTSTRAP_STATE_KEY="${PROD_BOOTSTRAP_STATE_KEY:-prod/bootstrap.tfstate}"
FOUNDATION_STATE_KEY="${PROD_FOUNDATION_STATE_KEY:-prod/foundation.tfstate}"
KUBECONFIG_PATH="${PROD_KUBECONFIG:-${HOME}/.kube/${PROJECT_NAME}-prod.yaml}"
WAIT_TIMEOUT="${PROD_WAIT_TIMEOUT:-20m}"
NLB_WAIT_ATTEMPTS="${PROD_NLB_WAIT_ATTEMPTS:-60}"
AWS_LB_CONTROLLER_CHART_VERSION="3.4.2"
RUNTIME_TF_DATA_ROOT="${PROD_TF_DATA_ROOT:-${HOME}/.cache/go-coffeeshop/terraform/prod-runtime-${EXPECTED_ACCOUNT_ID}}"
BOOTSTRAP_TF_DATA_DIR="${RUNTIME_TF_DATA_ROOT}/bootstrap"
FOUNDATION_TF_DATA_DIR="${RUNTIME_TF_DATA_ROOT}/foundation"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-prod.sh setup|teardown|g1|g2|g3|g4|wp2|reconcile

Default configuration:
  Account, Region, project and EKS API CIDRs are read from:
    infrastructure/terraform/envs/prod/terraform.tfvars
  Current hourly rates are queried from the AWS Price List API and combined
  with the WP2 resource quantities before any infrastructure apply.

Optional:
  PROD_AUTO_APPROVE=true             Disable Terraform approval prompts
  PROD_VAR_FILE                      Use another Terraform variable file
  PROD_STATE_BUCKET_NAME             Override the account-derived state bucket
  PROD_STATE_KMS_KEY_ID              Override the account-local state KMS key
  PROD_BOOTSTRAP_STATE_KEY           Default: prod/bootstrap.tfstate
  PROD_FOUNDATION_STATE_KEY          Default: prod/foundation.tfstate
  PROD_BOOTSTRAP_RECOVERY_STATE      Explicit state file for an interrupted
                                     first bootstrap; only accepted when the
                                     PROD bucket exists and its state key does not
  PROD_KUBECONFIG                    Dedicated PROD kubeconfig path
  PROD_NLB_WAIT_ATTEMPTS             Ten-second polling attempts. Default: 60
  PROD_CONFIRM_TEARDOWN              Must equal the 12-digit target account ID
                                     before teardown can create a destroy plan

Actions:
  setup
       Normal operator entry point. Run G1-G4 and full reconcile in order,
       stopping immediately at the first failed gate.
  teardown
       Normal operator exit point. Delete controller-owned ingress first,
       selectively destroy ephemeral foundation, then fail closed on
       billable-orphan inventory. Retains the PROD backend, ECR
       repositories/lifecycle policies and AWS Budget.

Recovery/debug actions:
  g1   Verify caller/Region/cost window; create or reopen isolated backend
  g2   Target VPC and EKS control plane only; verify cluster ACTIVE
  g3   Target required add-ons and managed nodes; verify node/add-on health
  g4   Deploy AWS Load Balancer Controller & NLB IP-target probe; verify NLB & HTTP reachability
  wp2  Run g1, g2 and g3 in order; stops immediately on any failed gate
  reconcile
       Create and review one non-targeted saved plan, apply that exact plan,
       then require an empty follow-up plan and healthy EKS/NLB runtime
EOF
}

fail() {
  echo "Error: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

require_nonempty() {
  local name="$1"
  local value="$2"
  [[ -n "${value}" ]] || fail "${name} is required"
}

terraform_apply() {
  local directory="$1"
  shift
  local approval_args=()
  if [[ "${PROD_AUTO_APPROVE:-false}" == "true" ]]; then
    approval_args=(-auto-approve)
  fi
  terraform_run "${directory}" apply -input=false "${approval_args[@]}" "$@"
}

confirm_saved_plan() {
  local expected_word="$1"
  local prompt="$2"
  local answer

  if [[ "${PROD_AUTO_APPROVE:-false}" == "true" ]]; then
    echo "PROD_AUTO_APPROVE=true: accepting reviewed ${expected_word} plan."
    return
  fi

  if ! read -r -p "${prompt} Type '${expected_word}' to continue: " answer; then
    fail "${expected_word} cancelled because no interactive confirmation was received"
  fi
  [[ "${answer}" == "${expected_word}" ]] || fail "${expected_word} cancelled by operator"
}

terraform_run() {
  local directory="$1"
  shift
  local data_dir
  if [[ "${directory}" == "${BOOTSTRAP_TF_DIR}" ]]; then
    data_dir="${BOOTSTRAP_TF_DATA_DIR}"
  elif [[ "${directory}" == "${PROD_TF_DIR}" ]]; then
    data_dir="${FOUNDATION_TF_DATA_DIR}"
  else
    fail "no isolated TF_DATA_DIR mapping for ${directory}"
  fi
  TF_DATA_DIR="${data_dir}" terraform -chdir="${directory}" "$@"
}

backend_args() {
  local key="$1"
  local kms_key_id="$2"
  printf '%s\0' \
    "-backend-config=bucket=${STATE_BUCKET_NAME}" \
    "-backend-config=key=${key}" \
    "-backend-config=region=${EXPECTED_REGION}" \
    "-backend-config=encrypt=true" \
    "-backend-config=kms_key_id=${kms_key_id}" \
    "-backend-config=use_lockfile=true"
}

init_remote_backend() {
  local directory="$1"
  local key="$2"
  local kms_key_id
  local init_args=()
  kms_key_id="${PROD_STATE_KMS_KEY_ID:-$(
    aws kms describe-key \
      --key-id "alias/${PROJECT_NAME}-state-key" \
      --query 'KeyMetadata.Arn' \
      --output text
  )}"
  require_nonempty "PROD state KMS key" "${kms_key_id}"
  mapfile -d '' init_args < <(backend_args "${key}" "${kms_key_id}")
  terraform_run "${directory}" init -reconfigure -input=false "${init_args[@]}"
}

validate_base_inputs() {
  [[ "${ACTION}" =~ ^(setup|teardown|g1|g2|g3|g4|wp2|reconcile)$ ]] || {
    usage >&2
    exit 2
  }
  require_nonempty "PROD_EXPECTED_AWS_ACCOUNT_ID" "${EXPECTED_ACCOUNT_ID}"
  [[ "${EXPECTED_ACCOUNT_ID}" =~ ^[0-9]{12}$ ]] || \
    fail "PROD_EXPECTED_AWS_ACCOUNT_ID must be a valid 12-digit account ID"
  require_nonempty "PROD_EXPECTED_AWS_REGION" "${EXPECTED_REGION}"
  require_nonempty "TF_VAR_cluster_endpoint_public_access_cidrs" \
    "${CLUSTER_PUBLIC_CIDRS}"

  [[ "${NLB_WAIT_ATTEMPTS}" =~ ^[1-9][0-9]*$ ]] || \
    fail "PROD_NLB_WAIT_ATTEMPTS must be a positive integer"

  for command_name in aws terraform kubectl helm curl jq awk mktemp cp rm sleep; do
    require_command "${command_name}"
  done

  export AWS_REGION="${EXPECTED_REGION}"
  export AWS_DEFAULT_REGION="${EXPECTED_REGION}"
  export TF_VAR_aws_region="${EXPECTED_REGION}"
  export TF_VAR_expected_aws_account_id="${EXPECTED_ACCOUNT_ID}"
  export TF_VAR_project_name="${PROJECT_NAME}"
  export TF_VAR_cluster_endpoint_public_access_cidrs="${CLUSTER_PUBLIC_CIDRS}"
  install -d -m 0700 \
    "${BOOTSTRAP_TF_DATA_DIR}" \
    "${FOUNDATION_TF_DATA_DIR}" \
    "$(dirname "${KUBECONFIG_PATH}")"
}

verify_caller() {
  local caller_identity current_account current_arn configured_region
  caller_identity="$(aws sts get-caller-identity --output json)"
  current_account="$(jq -r '.Account' <<<"${caller_identity}")"
  current_arn="$(jq -r '.Arn' <<<"${caller_identity}")"
  configured_region="$(aws configure get region || true)"

  [[ "${current_account}" == "${EXPECTED_ACCOUNT_ID}" ]] || \
    fail "caller account ${current_account} does not match explicit target ${EXPECTED_ACCOUNT_ID}"

  echo "Caller ARN:      ${current_arn}"
  echo "Target account:  ${EXPECTED_ACCOUNT_ID}"
  echo "Target Region:   ${EXPECTED_REGION}"
  if [[ -n "${configured_region}" && "${configured_region}" != "${EXPECTED_REGION}" ]]; then
    echo "Note: AWS profile Region is ${configured_region}; this run explicitly overrides it with ${EXPECTED_REGION}."
  fi
}

show_hourly_estimate() {
  local nlb_count=0
  local public_ipv4_count=1
  if [[ "${ACTION}" =~ ^(setup|g4|reconcile|teardown)$ ]]; then
    nlb_count=1
    public_ipv4_count=3
  fi

  "${PROJECT_ROOT}/scripts/estimate-prod-hourly-cost.sh" \
    "${EXPECTED_REGION}" \
    "${NODE_INSTANCE_TYPE}" \
    "${NODE_COUNT}" \
    "${NODE_DISK_GIB}" \
    "${nlb_count}" \
    "${public_ipv4_count}"
}

bootstrap_backend() {
  local recovery_state expected_recovery_bucket staging_dir source_file
  recovery_state="${PROD_BOOTSTRAP_RECOVERY_STATE:-}"

  if aws s3api head-bucket --bucket "${STATE_BUCKET_NAME}" >/dev/null 2>&1; then
    if aws s3api head-object --bucket "${STATE_BUCKET_NAME}" --key "${BOOTSTRAP_STATE_KEY}" >/dev/null 2>&1; then
      echo "Reopening existing bootstrap state s3://${STATE_BUCKET_NAME}/${BOOTSTRAP_STATE_KEY}."
      init_remote_backend "${BOOTSTRAP_TF_DIR}" "${BOOTSTRAP_STATE_KEY}"
      terraform_apply "${BOOTSTRAP_TF_DIR}"
      return
    fi

    [[ -n "${recovery_state}" ]] || \
      fail "state bucket exists but ${BOOTSTRAP_STATE_KEY} is absent; set PROD_BOOTSTRAP_RECOVERY_STATE to the matching interrupted-bootstrap state after reviewing its lineage"
    [[ -f "${recovery_state}" ]] || \
      fail "PROD_BOOTSTRAP_RECOVERY_STATE does not exist: ${recovery_state}"
    expected_recovery_bucket="$(jq -r '.outputs.config.value.bucket // empty' "${recovery_state}")"
    [[ "${expected_recovery_bucket}" == "${STATE_BUCKET_NAME}" ]] || \
      fail "recovery state belongs to ${expected_recovery_bucket}, not ${STATE_BUCKET_NAME}"

    echo "Recovering interrupted PROD bootstrap from an explicitly selected matching lineage."
    init_remote_backend "${BOOTSTRAP_TF_DIR}" "${BOOTSTRAP_STATE_KEY}"
    terraform_run "${BOOTSTRAP_TF_DIR}" state push "${recovery_state}"
    terraform_apply "${BOOTSTRAP_TF_DIR}"
  else
    # A backend cannot create its own bucket. Build the initial lineage in an
    # isolated sibling root without backend/move declarations, then upload it.
    # Keeping the sibling below bootstrap preserves ../../modules resolution.
    staging_dir="$(mktemp -d "${PROJECT_ROOT}/infrastructure/terraform/bootstrap/.prod-staging.XXXXXX")"
    echo "First-bootstrap staging directory: ${staging_dir}"
    echo "If bootstrap fails, preserve this directory for state recovery."
    for source_file in "${BOOTSTRAP_TF_DIR}"/*.tf; do
      case "$(basename "${source_file}")" in
        backend.tf|moved.tf) continue ;;
      esac
      cp "${source_file}" "${staging_dir}/"
    done
    if [[ -f "${BOOTSTRAP_TF_DIR}/.terraform.lock.hcl" ]]; then
      cp "${BOOTSTRAP_TF_DIR}/.terraform.lock.hcl" "${staging_dir}/"
    fi
    TF_DATA_DIR="${staging_dir}/.terraform" terraform -chdir="${staging_dir}" init -input=false

    local approval_args=()
    if [[ "${PROD_AUTO_APPROVE:-false}" == "true" ]]; then
      approval_args=(-auto-approve)
    fi
    TF_DATA_DIR="${staging_dir}/.terraform" terraform -chdir="${staging_dir}" \
      apply -input=false "${approval_args[@]}"
    expected_local_bucket="$(jq -r '.outputs.config.value.bucket // empty' "${staging_dir}/terraform.tfstate")"
    [[ "${expected_local_bucket}" == "${STATE_BUCKET_NAME}" ]] || \
      fail "bootstrap created unexpected bucket ${expected_local_bucket}"

    init_remote_backend "${BOOTSTRAP_TF_DIR}" "${BOOTSTRAP_STATE_KEY}"
    terraform_run "${BOOTSTRAP_TF_DIR}" state push "${staging_dir}/terraform.tfstate"
    aws s3api head-object --bucket "${STATE_BUCKET_NAME}" --key "${BOOTSTRAP_STATE_KEY}" >/dev/null || \
      fail "bootstrap state push was not observable in S3; preserve ${staging_dir} for recovery"
    rm -r -- "${staging_dir}"
  fi

  aws s3api head-object --bucket "${STATE_BUCKET_NAME}" --key "${BOOTSTRAP_STATE_KEY}" >/dev/null || \
    fail "bootstrap state was not observable in S3"
}

initialize_foundation_backend() {
  init_remote_backend "${PROD_TF_DIR}" "${FOUNDATION_STATE_KEY}"
  terraform_run "${PROD_TF_DIR}" validate -no-color
}

run_g1() {
  echo "=== G1: caller, hourly estimate and isolated state ==="
  verify_caller
  show_hourly_estimate
  bootstrap_backend
  initialize_foundation_backend
  echo "G1 PASSED: caller, hourly estimate and isolated S3 backends are ready."
}

run_g2() {
  local cluster_name expected_cluster_arn actual_cluster_arn status
  echo "=== G2: VPC and EKS control plane ==="
  verify_caller
  show_hourly_estimate
  initialize_foundation_backend

  terraform_apply "${PROD_TF_DIR}" \
    -target=module.vpc \
    -target=module.eks_cluster.aws_iam_role.cluster \
    -target=module.eks_cluster.aws_iam_role_policy_attachment.cluster_policy \
    -target=module.eks_cluster.aws_cloudwatch_log_group.cluster \
    -target=module.eks_cluster.aws_eks_cluster.this

  cluster_name="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_name)"
  expected_cluster_arn="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_arn)"
  aws eks wait cluster-active --name "${cluster_name}"
  actual_cluster_arn="$(aws eks describe-cluster --name "${cluster_name}" --query 'cluster.arn' --output text)"
  status="$(aws eks describe-cluster --name "${cluster_name}" --query 'cluster.status' --output text)"
  [[ "${actual_cluster_arn}" == "${expected_cluster_arn}" ]] || \
    fail "AWS returned cluster ARN ${actual_cluster_arn}, expected ${expected_cluster_arn}"
  [[ "${status}" == "ACTIVE" ]] || fail "cluster ${cluster_name} is ${status}, not ACTIVE"
  echo "G2 PASSED: ${expected_cluster_arn} is ACTIVE."
}

terraform_coredns_is_tainted() {
  terraform_run "${PROD_TF_DIR}" state pull | jq -e '
    .resources[]
    | select(
        .module == "module.eks_cluster"
        and .mode == "managed"
        and .type == "aws_eks_addon"
        and .name == "coredns"
      )
    | .instances[]
    | select(.status == "tainted")
  ' >/dev/null
}

wait_for_addon_active() {
  local cluster_name="$1"
  local addon_name="$2"
  local status health_issues

  if aws eks wait addon-active \
    --cluster-name "${cluster_name}" \
    --addon-name "${addon_name}"; then
    return
  fi

  status="$(aws eks describe-addon \
    --cluster-name "${cluster_name}" \
    --addon-name "${addon_name}" \
    --query 'addon.status' \
    --output text)"
  health_issues="$(aws eks describe-addon \
    --cluster-name "${cluster_name}" \
    --addon-name "${addon_name}" \
    --query 'addon.health.issues' \
    --output json)"
  fail "EKS add-on ${addon_name} remained ${status}; health issues: ${health_issues}"
}

reconcile_coredns_after_nodes() {
  local cluster_name="$1"
  local addon_status

  if addon_status="$(aws eks describe-addon \
    --cluster-name "${cluster_name}" \
    --addon-name coredns \
    --query 'addon.status' \
    --output text 2>/dev/null)"; then
    echo "Existing CoreDNS add-on is ${addon_status}; waiting for node-backed recovery."
    wait_for_addon_active "${cluster_name}" coredns

    if terraform_coredns_is_tainted; then
      echo "CoreDNS is ACTIVE in AWS; clearing only its interrupted-create taint before reconciliation."
      terraform_run "${PROD_TF_DIR}" untaint module.eks_cluster.aws_eks_addon.coredns
    fi
  fi

  terraform_apply "${PROD_TF_DIR}" \
    -target=module.eks_cluster.aws_eks_addon.coredns
  wait_for_addon_active "${cluster_name}" coredns
}

run_g3() {
  local cluster_name cluster_arn node_group_name node_status desired_size ready_count
  echo "=== G3: managed add-ons and nodes ==="
  verify_caller
  show_hourly_estimate
  initialize_foundation_backend

  # DaemonSet-based add-ons can be registered before nodes. CoreDNS cannot
  # become healthy without schedulable nodes, so it is reconciled only after
  # the managed node group and networking DaemonSets are healthy.
  terraform_apply "${PROD_TF_DIR}" \
    -target=module.eks_cluster.aws_iam_openid_connect_provider.this \
    -target=module.eks_cluster.aws_eks_addon.pod_identity \
    -target=module.eks_cluster.aws_eks_addon.vpc_cni \
    -target=module.eks_cluster.aws_eks_addon.kube_proxy \
    -target=module.eks_nodes

  cluster_name="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_name)"
  cluster_arn="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_arn)"
  node_group_name="$(terraform_run "${PROD_TF_DIR}" output -raw node_group_name)"
  aws eks wait nodegroup-active --cluster-name "${cluster_name}" --nodegroup-name "${node_group_name}"
  node_status="$(aws eks describe-nodegroup --cluster-name "${cluster_name}" --nodegroup-name "${node_group_name}" --query 'nodegroup.status' --output text)"
  desired_size="$(aws eks describe-nodegroup --cluster-name "${cluster_name}" --nodegroup-name "${node_group_name}" --query 'nodegroup.scalingConfig.desiredSize' --output text)"
  [[ "${node_status}" == "ACTIVE" ]] || fail "node group ${node_group_name} is ${node_status}, not ACTIVE"

  aws eks update-kubeconfig --name "${cluster_name}" --region "${EXPECTED_REGION}" \
    --kubeconfig "${KUBECONFIG_PATH}" --alias "${cluster_arn}"
  export KUBECONFIG="${KUBECONFIG_PATH}"
  kubectl wait --for=condition=Ready nodes --all --timeout="${WAIT_TIMEOUT}"
  ready_count="$(kubectl get nodes -o json | jq '[.items[] | select(any(.status.conditions[]; .type == "Ready" and .status == "True"))] | length')"
  [[ "${ready_count}" -ge "${desired_size}" ]] || \
    fail "only ${ready_count}/${desired_size} desired nodes are Ready"

  kubectl rollout status daemonset/aws-node -n kube-system --timeout="${WAIT_TIMEOUT}"
  kubectl rollout status daemonset/kube-proxy -n kube-system --timeout="${WAIT_TIMEOUT}"
  kubectl rollout status daemonset/eks-pod-identity-agent -n kube-system --timeout="${WAIT_TIMEOUT}"

  reconcile_coredns_after_nodes "${cluster_name}"
  kubectl rollout status deployment/coredns -n kube-system --timeout="${WAIT_TIMEOUT}"
  echo "G3 PASSED: ${ready_count}/${desired_size} nodes Ready and required managed add-ons rolled out."
}

run_g4() {
  local cluster_name cluster_arn vpc_id controller_role_arn association_list association_count association_id association_json
  local nlb_hostname load_balancers_json load_balancer_arn load_balancer_az_count
  local target_groups_json target_group_arn target_type target_health_json
  local ready_pods_json ready_pod_ips healthy_count healthy_az_count unexpected_target_count
  local http_response
  echo "=== G4: managed ingress probe (AWS Load Balancer Controller + NLB IP-target) ==="
  verify_caller
  show_hourly_estimate
  initialize_foundation_backend

  terraform_apply "${PROD_TF_DIR}" \
    -target=aws_ecr_repository.app \
    -target=aws_ecr_lifecycle_policy.app \
    -target=aws_iam_role.aws_lb_controller \
    -target=aws_iam_policy.aws_lb_controller_policy \
    -target=aws_iam_role_policy_attachment.aws_lb_controller_attach \
    -target=aws_eks_pod_identity_association.aws_lb_controller

  cluster_name="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_name)"
  cluster_arn="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_arn)"
  vpc_id="$(terraform_run "${PROD_TF_DIR}" output -raw vpc_id)"
  controller_role_arn="$(terraform_run "${PROD_TF_DIR}" output -raw aws_load_balancer_controller_role_arn)"
  [[ "${vpc_id}" =~ ^vpc-[0-9a-f]+$ ]] || \
    fail "Terraform returned an invalid VPC ID: ${vpc_id}"

  association_list="$(aws eks list-pod-identity-associations \
    --cluster-name "${cluster_name}" \
    --namespace kube-system \
    --service-account aws-load-balancer-controller \
    --output json)"
  association_count="$(jq '.associations | length' <<<"${association_list}")"
  [[ "${association_count}" -eq 1 ]] || \
    fail "expected exactly one Pod Identity association for kube-system/aws-load-balancer-controller, found ${association_count}"
  association_id="$(jq -r '.associations[0].associationId' <<<"${association_list}")"
  association_json="$(aws eks describe-pod-identity-association \
    --cluster-name "${cluster_name}" \
    --association-id "${association_id}" \
    --output json)"
  [[ "$(jq -r '.association.roleArn' <<<"${association_json}")" == "${controller_role_arn}" ]] || \
    fail "AWS Load Balancer Controller Pod Identity association has an unexpected role"

  aws eks update-kubeconfig --name "${cluster_name}" --region "${EXPECTED_REGION}" \
    --kubeconfig "${KUBECONFIG_PATH}" --alias "${cluster_arn}"
  export KUBECONFIG="${KUBECONFIG_PATH}"

  echo "Deploying pinned AWS Load Balancer Controller chart ${AWS_LB_CONTROLLER_CHART_VERSION}..."
  helm repo add eks https://aws.github.io/eks-charts --force-update
  helm upgrade --install aws-load-balancer-controller eks/aws-load-balancer-controller \
    --namespace kube-system \
    --version "${AWS_LB_CONTROLLER_CHART_VERSION}" \
    --values "${PROJECT_ROOT}/infrastructure/k8s/prod/ingress-probe/aws-load-balancer-controller-values.yaml" \
    --set-string clusterName="${cluster_name}" \
    --set-string region="${EXPECTED_REGION}" \
    --set-string vpcId="${vpc_id}" \
    --atomic \
    --wait \
    --timeout "${WAIT_TIMEOUT}"

  [[ "$(helm list --namespace kube-system --filter '^aws-load-balancer-controller$' --output json | jq -r '.[0].chart')" == \
    "aws-load-balancer-controller-${AWS_LB_CONTROLLER_CHART_VERSION}" ]] || \
    fail "deployed AWS Load Balancer Controller chart version does not match the reviewed pin"
  [[ "$(helm list --namespace kube-system --filter '^aws-load-balancer-controller$' --output json | jq -r '.[0].app_version')" == \
    "v${AWS_LB_CONTROLLER_CHART_VERSION}" ]] || \
    fail "deployed AWS Load Balancer Controller app version does not match the reviewed pin"
  kubectl rollout status deployment/aws-load-balancer-controller \
    -n kube-system --timeout="${WAIT_TIMEOUT}"

  echo "Deploying NLB Ingress Probe after the controller webhook is ready..."
  kubectl apply -f "${PROJECT_ROOT}/infrastructure/k8s/prod/ingress-probe/nlb-ingress-probe.yaml"
  echo "Waiting for NLB Ingress Probe deployment to become healthy..."
  kubectl rollout status deployment/nlb-ingress-probe -n ingress-probe --timeout="${WAIT_TIMEOUT}"

  ready_pods_json="$(kubectl get pods -n ingress-probe -l app=nlb-ingress-probe -o json)"
  ready_pod_ips="$(jq -c '[.items[] | select(any(.status.conditions[]?; .type == "Ready" and .status == "True")) | .status.podIP]' <<<"${ready_pods_json}")"
  [[ "$(jq 'length' <<<"${ready_pod_ips}")" -ge 2 ]] || \
    fail "fewer than two ready NLB probe Pod IPs were found"

  echo "Waiting for AWS Load Balancer Controller to provision NLB and assign external hostname..."
  nlb_hostname=""
  for ((i = 1; i <= NLB_WAIT_ATTEMPTS; i++)); do
    nlb_hostname="$(kubectl get svc nlb-ingress-probe-svc -n ingress-probe -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)"
    if [[ -n "${nlb_hostname}" ]]; then
      echo "NLB Hostname assigned: ${nlb_hostname}"
      break
    fi
    echo "Attempt ${i}/${NLB_WAIT_ATTEMPTS}: Waiting for NLB hostname assignment... (10s)"
    sleep 10
  done
  [[ -n "${nlb_hostname}" ]] || fail "AWS Load Balancer Controller failed to assign NLB hostname in time"

  load_balancers_json="$(aws elbv2 describe-load-balancers --region "${EXPECTED_REGION}" --output json)"
  load_balancer_arn="$(jq -r --arg dns "${nlb_hostname}" \
    '[.LoadBalancers[] | select(.DNSName == $dns and .Type == "network" and .Scheme == "internet-facing")] | if length == 1 then .[0].LoadBalancerArn else empty end' \
    <<<"${load_balancers_json}")"
  [[ -n "${load_balancer_arn}" ]] || \
    fail "could not resolve exactly one internet-facing NLB from Service hostname ${nlb_hostname}"
  load_balancer_az_count="$(jq -r --arg arn "${load_balancer_arn}" \
    '[.LoadBalancers[] | select(.LoadBalancerArn == $arn) | .AvailabilityZones[].ZoneName] | unique | length' \
    <<<"${load_balancers_json}")"
  [[ "${load_balancer_az_count}" -ge 2 ]] || \
    fail "NLB is attached to only ${load_balancer_az_count} Availability Zone(s)"

  target_groups_json="$(aws elbv2 describe-target-groups \
    --load-balancer-arn "${load_balancer_arn}" \
    --region "${EXPECTED_REGION}" \
    --output json)"
  target_group_arn="$(jq -r '[.TargetGroups[] | select(.TargetType == "ip")] | if length == 1 then .[0].TargetGroupArn else empty end' \
    <<<"${target_groups_json}")"
  [[ -n "${target_group_arn}" ]] || \
    fail "expected exactly one IP target group owned by ${load_balancer_arn}"
  target_type="$(jq -r --arg arn "${target_group_arn}" '.TargetGroups[] | select(.TargetGroupArn == $arn) | .TargetType' \
    <<<"${target_groups_json}")"
  [[ "${target_type}" == "ip" ]] || fail "NLB target group type is ${target_type}, expected ip"

  echo "Waiting for Pod IP targets across two AZs to become healthy..."
  healthy_count=0
  healthy_az_count=0
  unexpected_target_count=0
  for ((i = 1; i <= NLB_WAIT_ATTEMPTS; i++)); do
    target_health_json="$(aws elbv2 describe-target-health \
      --target-group-arn "${target_group_arn}" \
      --region "${EXPECTED_REGION}" \
      --output json)"
    healthy_count="$(jq '[.TargetHealthDescriptions[] | select(.TargetHealth.State == "healthy")] | length' \
      <<<"${target_health_json}")"
    healthy_az_count="$(jq '[.TargetHealthDescriptions[] | select(.TargetHealth.State == "healthy") | .Target.AvailabilityZone] | unique | length' \
      <<<"${target_health_json}")"
    unexpected_target_count="$(jq --argjson pod_ips "${ready_pod_ips}" \
      '[.TargetHealthDescriptions[] | select(.TargetHealth.State == "healthy" and (.Target.Id as $id | $pod_ips | index($id) | not))] | length' \
      <<<"${target_health_json}")"
    if [[ "${healthy_count}" -ge 2 && "${healthy_az_count}" -ge 2 && "${unexpected_target_count}" -eq 0 ]]; then
      break
    fi
    echo "Attempt ${i}/${NLB_WAIT_ATTEMPTS}: healthy=${healthy_count}, AZs=${healthy_az_count}, unexpected targets=${unexpected_target_count}. Waiting 10s..."
    sleep 10
  done
  [[ "${healthy_count}" -ge 2 ]] || fail "fewer than two NLB targets became healthy"
  [[ "${healthy_az_count}" -ge 2 ]] || fail "healthy NLB targets do not span two Availability Zones"
  [[ "${unexpected_target_count}" -eq 0 ]] || fail "target group contains healthy targets that are not current probe Pod IPs"

  echo "Verifying external HTTP reachability via NLB..."
  http_response=""
  for ((i = 1; i <= NLB_WAIT_ATTEMPTS; i++)); do
    http_response="$(curl --fail --silent --show-error --max-time 5 "http://${nlb_hostname}/" 2>/dev/null || true)"
    if [[ "${http_response}" == *"CoffeeShop PROD NLB Ingress Probe OK"* ]]; then
      echo "HTTP Reachability Success: Received '${http_response}'"
      break
    fi
    echo "Attempt ${i}/${NLB_WAIT_ATTEMPTS}: NLB HTTP endpoint not ready yet. Waiting 10s..."
    sleep 10
  done

  [[ "${http_response}" == *"CoffeeShop PROD NLB Ingress Probe OK"* ]] || \
    fail "NLB HTTP reachability probe failed for http://${nlb_hostname}/"

  echo "G4 PASSED: Managed NLB IP-target ingress probe is healthy and reachable across 2 AZs (${nlb_hostname})."
}

verify_reconciled_runtime() {
  local cluster_name cluster_arn node_group_name desired_size ready_count
  local nlb_hostname load_balancers_json load_balancer_arn nlb_az_count
  local target_groups_json target_group_arn target_health_json healthy_count healthy_az_count
  local http_response

  cluster_name="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_name)"
  cluster_arn="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_arn)"
  node_group_name="$(terraform_run "${PROD_TF_DIR}" output -raw node_group_name)"

  [[ "$(aws eks describe-cluster --name "${cluster_name}" --query 'cluster.status' --output text)" == "ACTIVE" ]] || \
    fail "cluster ${cluster_name} is not ACTIVE after full reconcile"
  [[ "$(aws eks describe-nodegroup \
    --cluster-name "${cluster_name}" \
    --nodegroup-name "${node_group_name}" \
    --query 'nodegroup.status' \
    --output text)" == "ACTIVE" ]] || \
    fail "node group ${node_group_name} is not ACTIVE after full reconcile"
  desired_size="$(aws eks describe-nodegroup \
    --cluster-name "${cluster_name}" \
    --nodegroup-name "${node_group_name}" \
    --query 'nodegroup.scalingConfig.desiredSize' \
    --output text)"

  aws eks update-kubeconfig --name "${cluster_name}" --region "${EXPECTED_REGION}" \
    --kubeconfig "${KUBECONFIG_PATH}" --alias "${cluster_arn}"
  export KUBECONFIG="${KUBECONFIG_PATH}"
  kubectl wait --for=condition=Ready nodes --all --timeout="${WAIT_TIMEOUT}"
  ready_count="$(kubectl get nodes -o json | jq \
    '[.items[] | select(any(.status.conditions[]?; .type == "Ready" and .status == "True"))] | length')"
  [[ "${ready_count}" -ge "${desired_size}" ]] || \
    fail "only ${ready_count}/${desired_size} desired nodes are Ready after reconcile"

  kubectl rollout status deployment/coredns -n kube-system --timeout="${WAIT_TIMEOUT}"
  kubectl rollout status deployment/aws-load-balancer-controller \
    -n kube-system --timeout="${WAIT_TIMEOUT}"
  kubectl rollout status deployment/nlb-ingress-probe \
    -n ingress-probe --timeout="${WAIT_TIMEOUT}"

  nlb_hostname="$(kubectl get service nlb-ingress-probe-svc \
    -n ingress-probe \
    -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')"
  require_nonempty "reconciled NLB hostname" "${nlb_hostname}"
  load_balancers_json="$(aws elbv2 describe-load-balancers --output json)"
  load_balancer_arn="$(jq -r --arg dns "${nlb_hostname}" \
    '[.LoadBalancers[] | select(.DNSName == $dns and .Type == "network" and .State.Code == "active")]
     | if length == 1 then .[0].LoadBalancerArn else empty end' \
    <<<"${load_balancers_json}")"
  [[ -n "${load_balancer_arn}" ]] || \
    fail "reconcile could not resolve exactly one ACTIVE NLB for ${nlb_hostname}"
  nlb_az_count="$(jq -r --arg arn "${load_balancer_arn}" \
    '[.LoadBalancers[] | select(.LoadBalancerArn == $arn) | .AvailabilityZones[].ZoneName]
     | unique | length' <<<"${load_balancers_json}")"
  [[ "${nlb_az_count}" -ge 2 ]] || \
    fail "reconciled NLB spans only ${nlb_az_count} Availability Zone(s)"

  target_groups_json="$(aws elbv2 describe-target-groups \
    --load-balancer-arn "${load_balancer_arn}" \
    --output json)"
  target_group_arn="$(jq -r \
    '[.TargetGroups[] | select(.TargetType == "ip")]
     | if length == 1 then .[0].TargetGroupArn else empty end' \
    <<<"${target_groups_json}")"
  [[ -n "${target_group_arn}" ]] || \
    fail "reconcile expected exactly one IP target group for ${load_balancer_arn}"
  target_health_json="$(aws elbv2 describe-target-health \
    --target-group-arn "${target_group_arn}" \
    --output json)"
  healthy_count="$(jq \
    '[.TargetHealthDescriptions[] | select(.TargetHealth.State == "healthy")] | length' \
    <<<"${target_health_json}")"
  healthy_az_count="$(jq \
    '[.TargetHealthDescriptions[]
      | select(.TargetHealth.State == "healthy")
      | .Target.AvailabilityZone]
     | unique | length' \
    <<<"${target_health_json}")"
  [[ "${healthy_count}" -ge 2 && "${healthy_az_count}" -ge 2 ]] || \
    fail "reconciled NLB has ${healthy_count} healthy target(s) across ${healthy_az_count} AZ(s)"

  http_response="$(curl --fail --silent --show-error --max-time 10 "http://${nlb_hostname}/")"
  [[ "${http_response}" == *"CoffeeShop PROD NLB Ingress Probe OK"* ]] || \
    fail "reconciled NLB returned an unexpected HTTP response"
  echo "Runtime verification passed: EKS/nodes/controller/probe healthy; ${healthy_count} NLB targets healthy across ${healthy_az_count} AZs."
}

run_reconcile() {
  local plan_dir plan_file plan_json follow_up_exit

  echo "=== WP4 reconcile: full non-targeted saved plan and runtime verification ==="
  verify_caller
  show_hourly_estimate
  initialize_foundation_backend

  plan_dir="$(mktemp -d)"
  plan_file="${plan_dir}/prod-reconcile.tfplan"
  plan_json="${plan_dir}/prod-reconcile.json"

  terraform_run "${PROD_TF_DIR}" plan -input=false -out="${plan_file}"
  terraform_run "${PROD_TF_DIR}" show -json "${plan_file}" >"${plan_json}"
  if jq -e '
    any(.resource_changes[]?;
      (.change.actions | index("delete")) != null
    )
  ' "${plan_json}" >/dev/null; then
    rm -r -- "${plan_dir}"
    fail "full reconcile plan contains delete/replacement actions; review manually before retrying"
  fi

  echo "Saved full reconcile plan (delete/replacement actions: 0):"
  terraform_run "${PROD_TF_DIR}" show -no-color "${plan_file}"
  confirm_saved_plan "reconcile" "Review the complete non-targeted plan above."
  terraform_run "${PROD_TF_DIR}" apply -input=false "${plan_file}"
  rm -r -- "${plan_dir}"

  set +e
  terraform_run "${PROD_TF_DIR}" plan -input=false -detailed-exitcode
  follow_up_exit=$?
  set -e
  case "${follow_up_exit}" in
    0) ;;
    2) fail "full desired state still has changes after reconcile" ;;
    *) fail "follow-up Terraform plan failed after reconcile" ;;
  esac

  verify_reconciled_runtime
  echo "WP4 RECONCILE PASSED: full plan applied exactly, follow-up plan is empty, and runtime remains healthy."
}

wait_for_nlb_deletion() {
  local load_balancer_arn="$1"
  local attempt

  [[ -n "${load_balancer_arn}" ]] || return
  for ((attempt = 1; attempt <= NLB_WAIT_ATTEMPTS; attempt++)); do
    if ! aws elbv2 describe-load-balancers \
      --load-balancer-arns "${load_balancer_arn}" \
      >/dev/null 2>&1; then
      echo "Controller-owned NLB deletion confirmed."
      return
    fi
    echo "Attempt ${attempt}/${NLB_WAIT_ATTEMPTS}: waiting for controller-owned NLB deletion... (10s)"
    sleep 10
  done
  fail "controller-owned NLB still exists; keep the controller running and diagnose finalizers/events"
}

tagged_resource_count() {
  local resource_type="$1"
  aws resourcegroupstaggingapi get-resources \
    --resource-type-filters "${resource_type}" \
    --tag-filters \
      "Key=Project,Values=${PROJECT_NAME}" \
      "Key=Environment,Values=${ENVIRONMENT}" \
      "Key=Phase,Values=PROD-1" \
    --query 'length(ResourceTagMappingList)' \
    --output text
}

verify_teardown_inventory() {
  local cluster_name="$1"
  local vpc_id="$2"
  local instance_count volume_count nat_count eip_count nlb_count target_group_count log_group_count
  local repository
  local retained_repositories=(
    go-coffeeshop-web
    go-coffeeshop-proxy
    go-coffeeshop-product
    go-coffeeshop-counter
    go-coffeeshop-barista
    go-coffeeshop-kitchen
    platform-ownership-guard
  )

  if aws eks describe-cluster --name "${cluster_name}" >/dev/null 2>&1; then
    fail "billable orphan inventory: EKS cluster ${cluster_name} still exists"
  fi
  if aws ec2 describe-vpcs --vpc-ids "${vpc_id}" >/dev/null 2>&1; then
    fail "billable orphan inventory: PROD VPC ${vpc_id} still exists"
  fi

  instance_count="$(aws ec2 describe-instances \
    --filters \
      "Name=tag:Project,Values=${PROJECT_NAME}" \
      "Name=tag:Environment,Values=${ENVIRONMENT}" \
      "Name=instance-state-name,Values=pending,running,shutting-down,stopping,stopped" \
    --query 'length(Reservations[].Instances[])' \
    --output text)"
  volume_count="$(aws ec2 describe-volumes \
    --filters \
      "Name=tag:Project,Values=${PROJECT_NAME}" \
      "Name=tag:Environment,Values=${ENVIRONMENT}" \
    --query 'length(Volumes)' \
    --output text)"
  nat_count="$(aws ec2 describe-nat-gateways \
    --filter \
      "Name=tag:Project,Values=${PROJECT_NAME}" \
      "Name=tag:Environment,Values=${ENVIRONMENT}" \
    --query "length(NatGateways[?State!=\`deleted\`])" \
    --output text)"
  eip_count="$(aws ec2 describe-addresses \
    --filters \
      "Name=tag:Project,Values=${PROJECT_NAME}" \
      "Name=tag:Environment,Values=${ENVIRONMENT}" \
    --query 'length(Addresses)' \
    --output text)"
  nlb_count="$(tagged_resource_count elasticloadbalancing:loadbalancer)"
  target_group_count="$(tagged_resource_count elasticloadbalancing:targetgroup)"
  log_group_count="$(aws logs describe-log-groups \
    --log-group-name-prefix "/aws/eks/${cluster_name}/cluster" \
    --query 'length(logGroups)' \
    --output text)"

  [[ "${instance_count}" -eq 0 ]] || fail "billable orphan inventory: ${instance_count} EC2 instance(s) remain"
  [[ "${volume_count}" -eq 0 ]] || fail "billable orphan inventory: ${volume_count} EBS volume(s) remain"
  [[ "${nat_count}" -eq 0 ]] || fail "billable orphan inventory: ${nat_count} NAT Gateway(s) remain"
  [[ "${eip_count}" -eq 0 ]] || fail "billable orphan inventory: ${eip_count} Elastic IP(s) remain"
  [[ "${nlb_count}" -eq 0 ]] || fail "billable orphan inventory: ${nlb_count} tagged load balancer(s) remain"
  [[ "${target_group_count}" -eq 0 ]] || \
    fail "billable orphan inventory: ${target_group_count} tagged target group(s) remain"
  [[ "${log_group_count}" -eq 0 ]] || \
    fail "billable orphan inventory: ${log_group_count} EKS control-plane log group(s) remain"

  aws s3api head-bucket --bucket "${STATE_BUCKET_NAME}" >/dev/null || \
    fail "retained backend bucket ${STATE_BUCKET_NAME} is missing"
  aws kms describe-key --key-id "alias/${PROJECT_NAME}-state-key" >/dev/null || \
    fail "retained backend KMS alias is missing"
  for repository in "${retained_repositories[@]}"; do
    aws ecr describe-repositories --repository-names "${repository}" >/dev/null || \
      fail "retained ECR repository ${repository} is missing"
    aws ecr get-lifecycle-policy --repository-name "${repository}" >/dev/null || \
      fail "retained ECR lifecycle policy for ${repository} is missing"
  done
  aws budgets describe-budget \
    --account-id "${EXPECTED_ACCOUNT_ID}" \
    --budget-name "${PROJECT_NAME}-${ENVIRONMENT}-monthly-budget" >/dev/null || \
    fail "retained AWS Budget is missing"

  echo "Immediate teardown inventory passed."
  echo "Retained allowlist confirmed: backend S3/KMS, 7 ECR repositories/lifecycle policies, AWS Budget."
  echo "Cost Explorer is delayed; estimate-versus-actual remains pending until billing data arrives."
}

run_teardown() {
  local cluster_name cluster_arn vpc_id nlb_hostname load_balancer_arn
  local plan_dir plan_file plan_json delete_count retained_delete_count unexpected_delete_count
  local destroy_targets=(
    -target=aws_eks_pod_identity_association.aws_lb_controller
    -target=aws_iam_role_policy_attachment.aws_lb_controller_attach
    -target=aws_iam_policy.aws_lb_controller_policy
    -target=aws_iam_role.aws_lb_controller
    -target=aws_iam_role_policy_attachment.github_delivery_attach
    -target=aws_iam_policy.github_delivery_policy
    -target=aws_iam_role.github_delivery_role
    -target=aws_iam_openid_connect_provider.github
    -target=module.eks_nodes
    -target=module.eks_cluster
    -target=module.vpc
  )

  echo "=== WP4 teardown: ingress-first selective destroy and orphan inventory ==="
  verify_caller
  [[ "${PROD_CONFIRM_TEARDOWN:-}" == "${EXPECTED_ACCOUNT_ID}" ]] || \
    fail "set PROD_CONFIRM_TEARDOWN=${EXPECTED_ACCOUNT_ID} to authorize this account-scoped teardown"
  show_hourly_estimate
  initialize_foundation_backend

  cluster_name="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_name)"
  cluster_arn="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_arn)"
  vpc_id="$(terraform_run "${PROD_TF_DIR}" output -raw vpc_id)"

  # Review and approve the exact Terraform destroy set before the first live
  # mutation. Kubernetes resources are outside this state, so deleting them
  # after approval does not invalidate the saved Terraform plan.
  plan_dir="$(mktemp -d)"
  plan_file="${plan_dir}/prod-teardown.tfplan"
  plan_json="${plan_dir}/prod-teardown.json"
  terraform_run "${PROD_TF_DIR}" plan \
    -destroy \
    -input=false \
    "${destroy_targets[@]}" \
    -out="${plan_file}"
  terraform_run "${PROD_TF_DIR}" show -json "${plan_file}" >"${plan_json}"
  delete_count="$(jq \
    '[.resource_changes[]? | select((.change.actions | index("delete")) != null)] | length' \
    "${plan_json}")"
  retained_delete_count="$(jq '
    [.resource_changes[]?
      | select((.change.actions | index("delete")) != null)
      | select(
          (.address | startswith("aws_ecr_repository.app"))
          or (.address | startswith("aws_ecr_lifecycle_policy.app"))
          or (.address == "aws_budgets_budget.prod_budget")
        )]
    | length
  ' "${plan_json}")"
  unexpected_delete_count="$(jq '
    [
      .resource_changes[]?
      | select((.change.actions | index("delete")) != null)
      | .address
      | select(
          (
            . == "aws_eks_pod_identity_association.aws_lb_controller"
            or . == "aws_iam_role_policy_attachment.aws_lb_controller_attach"
            or . == "aws_iam_policy.aws_lb_controller_policy"
            or . == "aws_iam_role.aws_lb_controller"
            or . == "aws_iam_role_policy_attachment.github_delivery_attach"
            or . == "aws_iam_policy.github_delivery_policy"
            or . == "aws_iam_role.github_delivery_role"
            or startswith("aws_iam_openid_connect_provider.github")
            or startswith("module.eks_nodes.")
            or startswith("module.eks_cluster.")
            or startswith("module.vpc.")
          ) | not
        )
    ] | length
  ' "${plan_json}")"
  if [[ "${delete_count}" -eq 0 ]]; then
    rm -r -- "${plan_dir}"
    fail "teardown plan contains no deletes; state may already be decommissioned"
  fi
  if [[ "${retained_delete_count}" -ne 0 ]]; then
    rm -r -- "${plan_dir}"
    fail "teardown plan attempts to delete retained ECR/Budget resources"
  fi
  if [[ "${unexpected_delete_count}" -ne 0 ]]; then
    rm -r -- "${plan_dir}"
    fail "teardown plan contains ${unexpected_delete_count} delete(s) outside the reviewed ephemeral allowlist"
  fi

  echo "Saved selective teardown plan: ${delete_count} reviewed ephemeral deletes; retained/unexpected deletes: 0"
  terraform_run "${PROD_TF_DIR}" show -no-color "${plan_file}"
  confirm_saved_plan "teardown" "Review the complete selective destroy plan above."

  aws eks update-kubeconfig --name "${cluster_name}" --region "${EXPECTED_REGION}" \
    --kubeconfig "${KUBECONFIG_PATH}" --alias "${cluster_arn}"
  export KUBECONFIG="${KUBECONFIG_PATH}"

  nlb_hostname="$(kubectl get service nlb-ingress-probe-svc \
    -n ingress-probe \
    -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)"
  load_balancer_arn=""
  if [[ -n "${nlb_hostname}" ]]; then
    kubectl rollout status deployment/aws-load-balancer-controller \
      -n kube-system --timeout="${WAIT_TIMEOUT}"
    load_balancer_arn="$(aws elbv2 describe-load-balancers \
      --query "LoadBalancers[?DNSName=='${nlb_hostname}'].LoadBalancerArn | [0]" \
      --output text)"
    [[ "${load_balancer_arn}" != "None" ]] || load_balancer_arn=""
    echo "Deleting ingress-probe namespace while its controller is healthy..."
    kubectl delete namespace ingress-probe --wait=true --timeout="${WAIT_TIMEOUT}"
    wait_for_nlb_deletion "${load_balancer_arn}"
  elif [[ "$(tagged_resource_count elasticloadbalancing:loadbalancer)" -ne 0 ]]; then
    fail "tagged PROD-1 load balancer exists but the owning Service cannot be found; diagnose before teardown"
  fi

  if helm status aws-load-balancer-controller -n kube-system >/dev/null 2>&1; then
    helm uninstall aws-load-balancer-controller \
      --namespace kube-system \
      --wait \
      --timeout "${WAIT_TIMEOUT}"
  fi

  terraform_run "${PROD_TF_DIR}" apply -input=false "${plan_file}"
  rm -r -- "${plan_dir}"

  verify_teardown_inventory "${cluster_name}" "${vpc_id}"
  echo "WP4 TEARDOWN PASSED: ephemeral foundation removed; retained allowlist and immediate inventory verified."
}

validate_base_inputs

case "${ACTION}" in
  setup)
    run_g1
    run_g2
    run_g3
    run_g4
    run_reconcile
    ;;
  teardown) run_teardown ;;
  g1) run_g1 ;;
  g2) run_g2 ;;
  g3) run_g3 ;;
  g4) run_g4 ;;
  wp2)
    run_g1
    run_g2
    run_g3
    ;;
  reconcile) run_reconcile ;;
esac

echo "Action ${ACTION} completed."
