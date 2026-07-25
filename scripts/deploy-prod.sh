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
GITHUB_REPOSITORY="${PROD_GITHUB_REPOSITORY:-$(read_tfvar_string github_repository)}"
GITOPS_REPO_URL="${PROD_GITOPS_REPO_URL:-https://github.com/${GITHUB_REPOSITORY}.git}"
GITOPS_REVISION="${PROD_GITOPS_REVISION:-HEAD}"
CLUSTER_PUBLIC_CIDRS="${TF_VAR_cluster_endpoint_public_access_cidrs:-$(read_tfvar_expression cluster_endpoint_public_access_cidrs)}"
NODE_INSTANCE_TYPES_EXPR="$(read_tfvar_expression node_instance_types)"
NODE_INSTANCE_TYPES_EXPR="${NODE_INSTANCE_TYPES_EXPR:-[\"t3.medium\"]}"
NODE_INSTANCE_TYPE="${PROD_NODE_INSTANCE_TYPE:-$(jq -r '.[0]' <<<"${NODE_INSTANCE_TYPES_EXPR}")}"
NODE_COUNT="${PROD_NODE_COUNT:-$(read_tfvar_expression node_desired_size)}"
NODE_COUNT="${NODE_COUNT:-3}"
NODE_DISK_GIB="${PROD_NODE_DISK_GIB:-$(read_tfvar_expression node_disk_size)}"
NODE_DISK_GIB="${NODE_DISK_GIB:-20}"
RDS_INSTANCE_CLASS="${PROD_RDS_INSTANCE_CLASS:-$(read_tfvar_string rds_instance_class)}"
RDS_INSTANCE_CLASS="${RDS_INSTANCE_CLASS:-db.t4g.micro}"
RDS_STORAGE_GIB="${PROD_RDS_STORAGE_GIB:-$(read_tfvar_expression rds_allocated_storage)}"
RDS_STORAGE_GIB="${RDS_STORAGE_GIB:-20}"
RDS_ENGINE_VERSION="${PROD_RDS_ENGINE_VERSION:-$(read_tfvar_string rds_engine_version)}"
RDS_ENGINE_VERSION="${RDS_ENGINE_VERSION:-16.14}"
CLUSTER_VERSION="${PROD_CLUSTER_VERSION:-$(read_tfvar_string cluster_version)}"
CLUSTER_VERSION="${CLUSTER_VERSION:-1.35}"
EBS_CSI_ADDON_VERSION="${PROD_EBS_CSI_ADDON_VERSION:-$(read_tfvar_string ebs_csi_addon_version)}"
EBS_CSI_ADDON_VERSION="${EBS_CSI_ADDON_VERSION:-v1.62.0-eksbuild.1}"
CLOUDWATCH_ADDON_VERSION="${PROD_CLOUDWATCH_ADDON_VERSION:-$(read_tfvar_string cloudwatch_observability_addon_version)}"
CLOUDWATCH_ADDON_VERSION="${CLOUDWATCH_ADDON_VERSION:-v6.4.0-eksbuild.1}"
STATE_BUCKET_NAME="${PROD_STATE_BUCKET_NAME:-${PROJECT_NAME}-terraform-state-${EXPECTED_ACCOUNT_ID}}"
BOOTSTRAP_STATE_KEY="${PROD_BOOTSTRAP_STATE_KEY:-prod/bootstrap.tfstate}"
FOUNDATION_STATE_KEY="${PROD_FOUNDATION_STATE_KEY:-prod/foundation.tfstate}"
KUBECONFIG_PATH="${PROD_KUBECONFIG:-${HOME}/.kube/${PROJECT_NAME}-prod.yaml}"
WAIT_TIMEOUT="${PROD_WAIT_TIMEOUT:-20m}"
POLL_ATTEMPTS="${PROD_POLL_ATTEMPTS:-60}"
RELEASE_POLL_ATTEMPTS="${PROD_RELEASE_POLL_ATTEMPTS:-360}"
AWS_LB_CONTROLLER_CHART_VERSION="3.4.2"
ARGO_CD_CHART_VERSION="6.7.18"
EXTERNAL_SECRETS_CHART_VERSION="2.5.0"
CERT_MANAGER_CHART_VERSION="v1.20.0"
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
  PROD_GITHUB_REPOSITORY             Override owner/repository from tfvars
  PROD_GITOPS_REPO_URL               Override the HTTPS repository read by Argo CD
  PROD_GITOPS_REVISION               Desired-state Git revision. Default: HEAD
  PROD_POLL_ATTEMPTS                 Ten-second runtime polling attempts. Default: 60
  PROD_RELEASE_POLL_ATTEMPTS         Release-set polling attempts. Default: 360
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
  g4   Provision delivery identity, deploy Argo CD and ALB IP-target ingress
  wp2  Run g1, g2 and g3 in order; stops immediately on any failed gate
  reconcile
       Create and review one non-targeted saved plan, apply that exact plan,
       then require an empty follow-up plan and healthy EKS/ALB runtime
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
  require_nonempty "github_repository" "${GITHUB_REPOSITORY}"
  [[ "${GITHUB_REPOSITORY}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || \
    fail "github_repository must use owner/repository form"
  [[ "${GITOPS_REVISION}" =~ ^[A-Za-z0-9._/-]+$ ]] || \
    fail "PROD_GITOPS_REVISION contains unsupported characters"
  require_nonempty "TF_VAR_cluster_endpoint_public_access_cidrs" \
    "${CLUSTER_PUBLIC_CIDRS}"

  [[ "${POLL_ATTEMPTS}" =~ ^[1-9][0-9]*$ ]] || \
    fail "PROD_POLL_ATTEMPTS must be a positive integer"
  [[ "${RELEASE_POLL_ATTEMPTS}" =~ ^[1-9][0-9]*$ ]] || \
    fail "PROD_RELEASE_POLL_ATTEMPTS must be a positive integer"

  for command_name in aws terraform kubectl helm curl jq awk git mktemp cp rm sleep; do
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

wait_for_promoted_digest() {
  local overlay_path digest_summary attempt
  overlay_path="infrastructure/k8s/apps/coffeeshop/overlays/prod/kustomization.yaml"

  echo "Delivery identity and ECR are ready."
  echo "Run 'PROD — Commission Full Stack Without DEV' once and merge its desired-state PR."
  echo "Waiting for ${GITOPS_REVISION} to contain seven immutable release digests..."
  for ((attempt = 1; attempt <= RELEASE_POLL_ATTEMPTS; attempt++)); do
    if git fetch --quiet "${GITOPS_REPO_URL}" "${GITOPS_REVISION}" 2>/dev/null; then
      digest_summary="$(
        git show "FETCH_HEAD:${overlay_path}" 2>/dev/null |
          awk '
            BEGIN { count=0; placeholders=0 }
            /^[[:space:]]*digest:[[:space:]]*sha256:/ {
              count++
              if ($2 == "sha256:0000000000000000000000000000000000000000000000000000000000000000") {
                placeholders++
              }
            }
            END {print count ":" placeholders}
          '
      )"
      if [[ "${digest_summary}" == "7:0" ]]; then
        echo "All seven immutable PROD release digests are present."
        return
      fi
    fi
    echo "Attempt ${attempt}/${RELEASE_POLL_ATTEMPTS}: release set is ${digest_summary:-unavailable}; expected 7:0. Waiting 10s..."
    sleep 10
  done
  fail "complete seven-component release set did not appear on ${GITOPS_REPO_URL}@${GITOPS_REVISION}"
}

apply_bootstrap_manifest() {
  local manifest="$1"
    sed \
      -e "s|__GITOPS_REPO_URL__|${GITOPS_REPO_URL}|g" \
      -e "s|__GITOPS_REVISION__|${GITOPS_REVISION}|g" \
      "${PROJECT_ROOT}/infrastructure/k8s/environments/prod/bootstrap/${manifest}" |
      kubectl apply -f -
}

wait_for_argocd_application() {
  local application="$1"
  local sync_status="" health_status=""
  for ((i = 1; i <= POLL_ATTEMPTS; i++)); do
    sync_status="$(kubectl get application "${application}" -n argocd -o jsonpath='{.status.sync.status}' 2>/dev/null || true)"
    health_status="$(kubectl get application "${application}" -n argocd -o jsonpath='{.status.health.status}' 2>/dev/null || true)"
    if [[ "${sync_status}" == "Synced" && "${health_status}" == "Healthy" ]]; then
      echo "Argo CD Application ${application} is Synced and Healthy."
      return
    fi
    echo "Attempt ${i}/${POLL_ATTEMPTS}: ${application} sync=${sync_status:-unknown}, health=${health_status:-unknown}. Waiting 10s..."
    sleep 10
  done
  fail "Argo CD Application ${application} did not become Synced and Healthy in time"
}

apply_rds_master_external_secret() {
  local master_secret_arn rds_address rds_port
  master_secret_arn="$(terraform_run "${PROD_TF_DIR}" output -raw rds_master_secret_arn)"
  rds_address="$(terraform_run "${PROD_TF_DIR}" output -raw rds_address)"
  rds_port="$(terraform_run "${PROD_TF_DIR}" output -raw rds_port)"
  [[ "${master_secret_arn}" == arn:aws:secretsmanager:* ]] || \
    fail "Terraform returned an invalid RDS master secret ARN"
  [[ "${rds_address}" =~ ^[A-Za-z0-9.-]+$ ]] || \
    fail "Terraform returned an invalid RDS address"
  [[ "${rds_port}" =~ ^[0-9]+$ ]] || \
    fail "Terraform returned an invalid RDS port"
  sed \
    -e "s|__RDS_MASTER_SECRET_ARN__|${master_secret_arn}|g" \
    -e "s|__RDS_ADDRESS__|${rds_address}|g" \
    -e "s|__RDS_PORT__|${rds_port}|g" \
    "${PROJECT_ROOT}/infrastructure/k8s/environments/prod/bootstrap/rds-master-external-secret.yaml.tpl" |
    kubectl apply -f -
}

remote_promoted_digest() {
  local overlay_path
  overlay_path="infrastructure/k8s/apps/coffeeshop/overlays/prod/kustomization.yaml"
  git fetch --quiet "${GITOPS_REPO_URL}" "${GITOPS_REVISION}"
  git show "FETCH_HEAD:${overlay_path}" |
    awk '/^[[:space:]]*digest:[[:space:]]*sha256:/ {print $2; exit}'
}

verify_argocd_self_heal() {
  echo "Verifying Argo CD self-heal by drifting web replicas to zero..."
  kubectl patch deployment web -n coffeeshop --type=merge -p '{"spec":{"replicas":0}}'
  for ((i = 1; i <= POLL_ATTEMPTS; i++)); do
    if [[ "$(kubectl get deployment web -n coffeeshop -o jsonpath='{.spec.replicas}' 2>/dev/null || true)" == "1" ]]; then
      kubectl rollout status deployment/web -n coffeeshop --timeout="${WAIT_TIMEOUT}"
      echo "Argo CD self-heal restored the Git-declared replica count."
      return
    fi
    echo "Attempt ${i}/${POLL_ATTEMPTS}: waiting for Argo CD self-heal. Waiting 10s..."
    sleep 10
  done
  fail "Argo CD did not self-heal the drifted web Deployment"
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

  bash "${PROJECT_ROOT}/scripts/estimate-prod-hourly-cost.sh" \
    "${EXPECTED_REGION}" \
    "${NODE_INSTANCE_TYPE}" \
    "${NODE_COUNT}" \
    "${NODE_DISK_GIB}" \
    "${nlb_count}" \
    "${public_ipv4_count}" \
    "${RDS_INSTANCE_CLASS}" \
    "${RDS_STORAGE_GIB}" \
    15
}

verify_capacity_envelope() {
  local instance_json vcpu_per_node memory_mib_per_node
  local allocatable_cpu_millicores allocatable_memory_mib
  local required_cpu_millicores=2350
  local required_memory_mib=5376

  instance_json="$(aws ec2 describe-instance-types \
    --instance-types "${NODE_INSTANCE_TYPE}" \
    --query 'InstanceTypes[0].{vcpus:VCpuInfo.DefaultVCpus,memory:MemoryInfo.SizeInMiB}' \
    --output json)"
  vcpu_per_node="$(jq -r '.vcpus' <<<"${instance_json}")"
  memory_mib_per_node="$(jq -r '.memory' <<<"${instance_json}")"
  [[ "${vcpu_per_node}" =~ ^[1-9][0-9]*$ &&
     "${memory_mib_per_node}" =~ ^[1-9][0-9]*$ ]] || \
    fail "could not resolve capacity for ${NODE_INSTANCE_TYPE}"

  # Conservative 25% reserve for kubelet, OS and scheduling variance.
  allocatable_cpu_millicores=$((vcpu_per_node * NODE_COUNT * 750))
  allocatable_memory_mib=$((memory_mib_per_node * NODE_COUNT * 75 / 100))
  echo "Capacity preflight (${NODE_COUNT} x ${NODE_INSTANCE_TYPE}, 25% reserve):"
  echo "  schedulable CPU    : ${allocatable_cpu_millicores}m; designed requests ${required_cpu_millicores}m"
  echo "  schedulable memory : ${allocatable_memory_mib}Mi; designed requests ${required_memory_mib}Mi"
  ((allocatable_cpu_millicores >= required_cpu_millicores)) || \
    fail "designed CPU requests exceed the reviewed capacity envelope"
  ((allocatable_memory_mib >= required_memory_mib)) || \
    fail "designed memory requests exceed the reviewed capacity envelope"
}

verify_managed_service_availability() {
  local rds_option_count addon_contract addon_name expected_version addon_version
  rds_option_count="$(aws rds describe-orderable-db-instance-options \
    --engine postgres \
    --engine-version "${RDS_ENGINE_VERSION}" \
    --db-instance-class "${RDS_INSTANCE_CLASS}" \
    --query 'length(OrderableDBInstanceOptions)' \
    --output text)"
  [[ "${rds_option_count}" -ge 1 ]] || \
    fail "PostgreSQL ${RDS_ENGINE_VERSION}/${RDS_INSTANCE_CLASS} is not orderable in ${EXPECTED_REGION}"

  for addon_contract in \
    "aws-ebs-csi-driver:${EBS_CSI_ADDON_VERSION}" \
    "amazon-cloudwatch-observability:${CLOUDWATCH_ADDON_VERSION}"; do
    addon_name="${addon_contract%%:*}"
    expected_version="${addon_contract#*:}"
    addon_version="$(aws eks describe-addon-versions \
      --addon-name "${addon_name}" \
      --kubernetes-version "${CLUSTER_VERSION}" \
      --query "addons[0].addonVersions[?addonVersion=='${expected_version}'].addonVersion | [0]" \
      --output text)"
    [[ "${addon_version}" == "${expected_version}" ]] || \
      fail "${addon_name} ${expected_version} is not compatible with EKS ${CLUSTER_VERSION}"
  done
  echo "Managed service preflight passed: PostgreSQL ${RDS_ENGINE_VERSION}, EBS CSI ${EBS_CSI_ADDON_VERSION}, CloudWatch ${CLOUDWATCH_ADDON_VERSION}."
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
  verify_capacity_envelope
  verify_managed_service_availability
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

  echo "Applying PROD-3 managed stateful and observability foundation..."
  terraform_apply "${PROD_TF_DIR}" \
    -target=aws_db_instance.postgres \
    -target=aws_secretsmanager_secret.coffeeshop_app_secret \
    -target=aws_iam_role.eso_role \
    -target=aws_iam_policy.eso_policy \
    -target=aws_iam_role_policy_attachment.eso_attach \
    -target=aws_eks_pod_identity_association.eso \
    -target=aws_iam_role.cloudwatch_agent_role \
    -target=aws_iam_role_policy_attachment.cloudwatch_agent_attach \
    -target=aws_eks_pod_identity_association.cloudwatch_agent \
    -target=aws_iam_role.ebs_csi_role \
    -target=aws_iam_role_policy_attachment.ebs_csi_attach \
    -target=aws_eks_pod_identity_association.ebs_csi \
    -target=aws_eks_addon.ebs_csi \
    -target=aws_eks_addon.cloudwatch_observability \
    -target=aws_cloudwatch_log_group.application_logs \
    -target=aws_cloudwatch_log_group.host_logs \
    -target=aws_cloudwatch_log_group.dataplane_logs \
    -target=aws_cloudwatch_metric_alarm.rds_free_storage \
    -target=aws_cloudwatch_metric_alarm.node_cpu_high

  bash "${PROJECT_ROOT}/scripts/seed-prod-secrets.sh" \
    "${EXPECTED_REGION}" \
    "$(terraform_run "${PROD_TF_DIR}" output -raw coffeeshop_app_secret_arn)" \
    "$(terraform_run "${PROD_TF_DIR}" output -raw rds_endpoint)"

  aws eks wait addon-active --cluster-name "${cluster_name}" --addon-name aws-ebs-csi-driver
  aws eks wait addon-active --cluster-name "${cluster_name}" --addon-name amazon-cloudwatch-observability
  [[ "$(aws rds describe-db-instances \
    --db-instance-identifier "${PROJECT_NAME}-${ENVIRONMENT}-db" \
    --query 'DBInstances[0].DBInstanceStatus' --output text)" == "available" ]] || \
    fail "RDS instance did not become available"
  echo "G3 PASSED: nodes, RDS, seeded secret, EBS CSI and scoped CloudWatch foundation are ready."
}

run_g4() {
  local cluster_name cluster_arn vpc_id controller_role_arn association_list association_count association_id association_json
  local load_balancers_json load_balancer_arn load_balancer_az_count
  local target_groups_json target_group_arn target_health_json listener_json web_service_port
  local healthy_count healthy_az_count ready_pod_ips healthy_target_ips http_response
  echo "=== G4: protected delivery, Argo CD and ALB IP-target ingress ==="
  verify_caller
  show_hourly_estimate
  initialize_foundation_backend

  terraform_apply "${PROD_TF_DIR}" \
    -target=aws_ecr_repository.app \
    -target=aws_ecr_lifecycle_policy.app \
    -target=aws_iam_openid_connect_provider.github \
    -target=aws_iam_role.github_delivery_role \
    -target=aws_iam_policy.github_delivery_policy \
    -target=aws_iam_role_policy_attachment.github_delivery_attach \
    -target=aws_iam_role.aws_lb_controller \
    -target=aws_iam_policy.aws_lb_controller_policy \
    -target=aws_iam_role_policy_attachment.aws_lb_controller_attach \
    -target=aws_eks_pod_identity_association.aws_lb_controller

  cluster_name="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_name)"
  cluster_arn="$(terraform_run "${PROD_TF_DIR}" output -raw cluster_arn)"
  vpc_id="$(terraform_run "${PROD_TF_DIR}" output -raw vpc_id)"
  controller_role_arn="$(terraform_run "${PROD_TF_DIR}" output -raw aws_load_balancer_controller_role_arn)"
  echo "GitHub Environment PROD_AWS_ROLE_ARN: $(terraform_run "${PROD_TF_DIR}" output -raw github_delivery_role_arn)"
  echo "GitHub Environment PROD_AWS_REGION:   ${EXPECTED_REGION}"
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
    --values "${PROJECT_ROOT}/infrastructure/k8s/environments/prod/platform/aws-load-balancer-controller-values.yaml" \
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

  echo "Deploying pinned cert-manager and External Secrets Operator..."
  helm repo add jetstack https://charts.jetstack.io --force-update
  helm upgrade --install cert-manager jetstack/cert-manager \
    --namespace cert-manager \
    --create-namespace \
    --version "${CERT_MANAGER_CHART_VERSION}" \
    --set installCRDs=true \
    --atomic \
    --wait \
    --timeout "${WAIT_TIMEOUT}"

  helm repo add external-secrets https://charts.external-secrets.io --force-update
  helm upgrade --install external-secrets external-secrets/external-secrets \
    --namespace external-secrets \
    --create-namespace \
    --version "${EXTERNAL_SECRETS_CHART_VERSION}" \
    --set installCRDs=true \
    --set serviceAccount.name=external-secrets-sa \
    --atomic \
    --wait \
    --timeout "${WAIT_TIMEOUT}"
  kubectl rollout status deployment/external-secrets \
    -n external-secrets --timeout="${WAIT_TIMEOUT}"

  echo "Deploying the pinned RabbitMQ Cluster Operator manifest..."
  kubectl apply --server-side --force-conflicts \
    -f "${PROJECT_ROOT}/infrastructure/k8s/environments/dev/gitops/addons/rabbitmq-operator/cluster-operator.yaml"
  kubectl rollout status deployment/rabbitmq-cluster-operator \
    -n rabbitmq-system --timeout="${WAIT_TIMEOUT}"

  echo "Deploying pinned Argo CD chart ${ARGO_CD_CHART_VERSION}..."
  helm repo add argo https://argoproj.github.io/argo-helm --force-update
  helm upgrade --install argocd argo/argo-cd \
    --namespace argocd \
    --create-namespace \
    --version "${ARGO_CD_CHART_VERSION}" \
    --values "${PROJECT_ROOT}/infrastructure/k8s/environments/prod/platform/argocd-values.yaml" \
    --atomic \
    --wait \
    --timeout "${WAIT_TIMEOUT}"

  kubectl rollout status deployment/argocd-server -n argocd --timeout="${WAIT_TIMEOUT}"
  kubectl rollout status deployment/argocd-repo-server -n argocd --timeout="${WAIT_TIMEOUT}"
  kubectl rollout status statefulset/argocd-application-controller -n argocd --timeout="${WAIT_TIMEOUT}"

  wait_for_promoted_digest
  echo "Applying bounded AppProject and platform desired state first..."
  apply_bootstrap_manifest appproject.yaml
  apply_bootstrap_manifest coffeeshop-prod-platform-app.yaml

  for ((i = 1; i <= POLL_ATTEMPTS; i++)); do
    if kubectl get clustersecretstore aws-secretsmanager >/dev/null 2>&1; then
      break
    fi
    echo "Attempt ${i}/${POLL_ATTEMPTS}: waiting for ClusterSecretStore. Waiting 10s..."
    sleep 10
  done
  kubectl get clustersecretstore aws-secretsmanager >/dev/null 2>&1 || \
    fail "platform Application did not create ClusterSecretStore"

  apply_rds_master_external_secret
  kubectl wait --for=condition=Ready clustersecretstore/aws-secretsmanager \
    --timeout="${WAIT_TIMEOUT}"
  kubectl wait --for=condition=Ready externalsecret/coffeeshop-secret \
    -n coffeeshop --timeout="${WAIT_TIMEOUT}"
  kubectl wait --for=condition=Ready externalsecret/coffeeshop-rabbitmq-default-user \
    -n coffeeshop --timeout="${WAIT_TIMEOUT}"
  kubectl wait --for=condition=Ready externalsecret/coffeeshop-rds-master-bootstrap \
    -n coffeeshop --timeout="${WAIT_TIMEOUT}"

  wait_for_argocd_application coffeeshop-prod-platform
  kubectl wait --for=condition=AllReplicasReady rabbitmqcluster/coffeeshop-rabbitmq \
    -n coffeeshop --timeout="${WAIT_TIMEOUT}"

  echo "Platform and stateful dependencies are ready; applying CoffeeShop application."
  apply_bootstrap_manifest coffeeshop-prod-app.yaml
  wait_for_argocd_application coffeeshop-prod

  echo "Waiting for AWS Load Balancer Controller to provision ALB and assign external hostname..."
  alb_hostname=""
  for ((i = 1; i <= POLL_ATTEMPTS; i++)); do
    alb_hostname="$(kubectl get ingress coffeeshop-prod-alb-ingress -n coffeeshop -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)"
    if [[ -n "${alb_hostname}" ]]; then
      echo "ALB Hostname assigned: ${alb_hostname}"
      break
    fi
    echo "Attempt ${i}/${POLL_ATTEMPTS}: Waiting for ALB hostname assignment... (10s)"
    sleep 10
  done
  [[ -n "${alb_hostname}" ]] || fail "AWS Load Balancer Controller failed to assign ALB hostname in time"

  load_balancers_json="$(aws elbv2 describe-load-balancers --region "${EXPECTED_REGION}" --output json)"
  load_balancer_arn="$(jq -r --arg dns "${alb_hostname}" \
    '[.LoadBalancers[] | select(.DNSName == $dns and .Type == "application" and .Scheme == "internet-facing")] | if length == 1 then .[0].LoadBalancerArn else empty end' \
    <<<"${load_balancers_json}")"
  [[ -n "${load_balancer_arn}" ]] || \
    fail "could not resolve exactly one internet-facing ALB from Ingress hostname ${alb_hostname}"

  load_balancer_az_count="$(jq -r --arg arn "${load_balancer_arn}" \
    '[.LoadBalancers[] | select(.LoadBalancerArn == $arn) | .AvailabilityZones[].ZoneName] | unique | length' \
    <<<"${load_balancers_json}")"
  [[ "${load_balancer_az_count}" -ge 2 ]] || \
    fail "ALB is attached to only ${load_balancer_az_count} Availability Zone(s)"

  target_groups_json="$(aws elbv2 describe-target-groups \
    --load-balancer-arn "${load_balancer_arn}" \
    --region "${EXPECTED_REGION}" \
    --output json)"
  web_service_port="$(kubectl get service web -n coffeeshop \
    -o jsonpath='{.spec.ports[0].port}')"
  [[ "${web_service_port}" =~ ^[0-9]+$ ]] || \
    fail "could not discover the web Service port"
  target_group_arn="$(jq -r --argjson port "${web_service_port}" \
    '[.TargetGroups[] | select(.TargetType == "ip" and .Port == $port)]
     | if length == 1 then .[0].TargetGroupArn else empty end' \
    <<<"${target_groups_json}")"
  [[ -n "${target_group_arn}" ]] || \
    fail "expected exactly one IP target group for web Service port ${web_service_port}"
  listener_json="$(aws elbv2 describe-listeners \
    --load-balancer-arn "${load_balancer_arn}" \
    --region "${EXPECTED_REGION}" \
    --output json)"
  [[ "$(jq '[.Listeners[] | select(.Port == 80 and .Protocol == "HTTP")] | length' <<<"${listener_json}")" -eq 1 ]] || \
    fail "expected exactly one HTTP:80 listener on ${load_balancer_arn}"

  echo "Waiting for healthy ALB targets that exactly match Ready web Pod IPs..."
  healthy_count=0
  healthy_az_count=0
  for ((i = 1; i <= POLL_ATTEMPTS; i++)); do
    target_health_json="$(aws elbv2 describe-target-health \
      --target-group-arn "${target_group_arn}" \
      --region "${EXPECTED_REGION}" \
      --output json)"
    healthy_count="$(jq '[.TargetHealthDescriptions[] | select(.TargetHealth.State == "healthy")] | length' \
      <<<"${target_health_json}")"
    healthy_az_count="$(jq '[.TargetHealthDescriptions[] | select(.TargetHealth.State == "healthy") | .Target.AvailabilityZone] | unique | length' \
      <<<"${target_health_json}")"
    if [[ "${healthy_count}" -ge 1 && "${healthy_az_count}" -ge 1 ]]; then
      break
    fi
    echo "Attempt ${i}/${POLL_ATTEMPTS}: ALB healthy targets=${healthy_count}, AZs=${healthy_az_count}. Waiting 10s..."
    sleep 10
  done
  [[ "${healthy_count}" -ge 1 ]] || fail "no ALB targets became healthy"
  ready_pod_ips="$(kubectl get pods -n coffeeshop -l app=web -o json |
    jq -c '[.items[]
      | select(any(.status.conditions[]?; .type == "Ready" and .status == "True"))
      | .status.podIP] | sort')"
  healthy_target_ips="$(jq -c \
    '[.TargetHealthDescriptions[]
      | select(.TargetHealth.State == "healthy")
      | .Target.Id] | sort' <<<"${target_health_json}")"
  [[ "${healthy_target_ips}" == "${ready_pod_ips}" ]] || \
    fail "healthy ALB target IPs ${healthy_target_ips} do not match Ready web Pod IPs ${ready_pod_ips}"

  echo "Verifying external HTTP reachability via ALB..."
  http_response=""
  for ((i = 1; i <= POLL_ATTEMPTS; i++)); do
    http_response="$(curl --fail --silent --show-error --max-time 5 "http://${alb_hostname}/" 2>/dev/null || true)"
    if [[ -n "${http_response}" ]]; then
      echo "HTTP Reachability Success via ALB: Received response from ${alb_hostname}"
      break
    fi
    echo "Attempt ${i}/${POLL_ATTEMPTS}: ALB HTTP endpoint not ready yet. Waiting 10s..."
    sleep 10
  done
  grep -Fq 'CoffeeShop POS' <<<"${http_response}" || \
    fail "ALB endpoint did not return the expected CoffeeShop POS marker"

  for deployment in web proxy product counter barista kitchen; do
    kubectl rollout status "deployment/${deployment}" \
      -n coffeeshop --timeout="${WAIT_TIMEOUT}"
  done

  item_types_response="$(curl --fail --silent --show-error --max-time 10 \
    "http://${alb_hostname}/api/v1/api/item-types")"
  jq -e '.itemTypes | type == "array" and length > 0' >/dev/null <<<"${item_types_response}" || \
    fail "full transaction probe could not load product item types through /api"
  order_response="$(curl --fail --silent --show-error --max-time 15 \
    -H 'Content-Type: application/json' \
    -d '{"loyaltyMemberId":"01234567-89ab-cdef-0123-456789abcdef","timestamp":"2026-07-25T00:00:00Z","baristaItems":[{"itemType":0}]}' \
    "http://${alb_hostname}/api/v1/api/orders")"
  jq -e 'type == "object"' >/dev/null <<<"${order_response}" || \
    fail "full transaction order probe returned a non-JSON response"

  echo "G4 PASSED: managed platform, stateful services and full CoffeeShop release are healthy through ALB (${alb_hostname})."
}

verify_reconciled_runtime() {
  local cluster_name cluster_arn node_group_name desired_size ready_count
  local alb_hostname load_balancers_json load_balancer_arn alb_az_count
  local target_groups_json target_group_arn target_health_json listener_json web_service_port
  local healthy_count healthy_az_count ready_pod_ips healthy_target_ips
  local desired_digest git_digest running_image_id http_response

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
  kubectl rollout status deployment/argocd-server -n argocd --timeout="${WAIT_TIMEOUT}"
  kubectl rollout status statefulset/argocd-application-controller \
    -n argocd --timeout="${WAIT_TIMEOUT}"

  [[ "$(kubectl get application coffeeshop-prod -n argocd -o jsonpath='{.status.sync.status}' 2>/dev/null)" == "Synced" ]] || \
    fail "Argo CD application coffeeshop-prod is not Synced after reconcile"
  [[ "$(kubectl get application coffeeshop-prod -n argocd -o jsonpath='{.status.health.status}' 2>/dev/null)" == "Healthy" ]] || \
    fail "Argo CD application coffeeshop-prod is not Healthy after reconcile"

  alb_hostname="$(kubectl get ingress coffeeshop-prod-alb-ingress \
    -n coffeeshop \
    -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')"
  require_nonempty "reconciled ALB hostname" "${alb_hostname}"
  load_balancers_json="$(aws elbv2 describe-load-balancers --output json)"
  load_balancer_arn="$(jq -r --arg dns "${alb_hostname}" \
    '[.LoadBalancers[] | select(.DNSName == $dns and .Type == "application" and .State.Code == "active")]
     | if length == 1 then .[0].LoadBalancerArn else empty end' \
    <<<"${load_balancers_json}")"
  [[ -n "${load_balancer_arn}" ]] || \
    fail "reconcile could not resolve exactly one ACTIVE ALB for ${alb_hostname}"
  alb_az_count="$(jq -r --arg arn "${load_balancer_arn}" \
    '[.LoadBalancers[] | select(.LoadBalancerArn == $arn) | .AvailabilityZones[].ZoneName]
     | unique | length' <<<"${load_balancers_json}")"
  [[ "${alb_az_count}" -ge 2 ]] || \
    fail "reconciled ALB spans only ${alb_az_count} Availability Zone(s)"

  target_groups_json="$(aws elbv2 describe-target-groups \
    --load-balancer-arn "${load_balancer_arn}" \
    --output json)"
  web_service_port="$(kubectl get service web -n coffeeshop \
    -o jsonpath='{.spec.ports[0].port}')"
  [[ "${web_service_port}" =~ ^[0-9]+$ ]] || \
    fail "reconcile could not discover the web Service port"
  target_group_arn="$(jq -r --argjson port "${web_service_port}" \
    '[.TargetGroups[] | select(.TargetType == "ip" and .Port == $port)]
     | if length == 1 then .[0].TargetGroupArn else empty end' \
    <<<"${target_groups_json}")"
  [[ -n "${target_group_arn}" ]] || \
    fail "reconcile expected exactly one IP target group for web Service port ${web_service_port}"
  listener_json="$(aws elbv2 describe-listeners \
    --load-balancer-arn "${load_balancer_arn}" \
    --output json)"
  [[ "$(jq '[.Listeners[] | select(.Port == 80 and .Protocol == "HTTP")] | length' <<<"${listener_json}")" -eq 1 ]] || \
    fail "reconciled ALB does not have exactly one HTTP:80 listener"
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
  [[ "${healthy_count}" -ge 1 ]] || \
    fail "reconciled ALB target group has no healthy targets"
  ready_pod_ips="$(kubectl get pods -n coffeeshop -l app=web -o json |
    jq -c '[.items[]
      | select(any(.status.conditions[]?; .type == "Ready" and .status == "True"))
      | .status.podIP] | sort')"
  healthy_target_ips="$(jq -c \
    '[.TargetHealthDescriptions[]
      | select(.TargetHealth.State == "healthy")
      | .Target.Id] | sort' <<<"${target_health_json}")"
  [[ "${healthy_target_ips}" == "${ready_pod_ips}" ]] || \
    fail "reconciled healthy target IPs do not match Ready web Pod IPs"

  desired_digest="$(kubectl get deployment web -n coffeeshop \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="web")].image}' |
    awk -F@ '{print $2}')"
  [[ "${desired_digest}" =~ ^sha256:[0-9a-f]{64}$ ]] || \
    fail "web Deployment is not pinned to a valid immutable digest"
  git_digest="$(remote_promoted_digest)"
  [[ "${git_digest}" == "${desired_digest}" ]] || \
    fail "Git desired digest ${git_digest} does not match Deployment digest ${desired_digest}"
  aws ecr describe-images \
    --repository-name go-coffeeshop-web \
    --image-ids "imageDigest=${desired_digest}" \
    --output json >/dev/null || \
    fail "desired digest ${desired_digest} is not present in PROD ECR"
  running_image_id="$(kubectl get pods -n coffeeshop -l app=web -o json |
    jq -r '[.items[].status.containerStatuses[]
      | select(.name == "web")
      | .imageID
      | split("@")[1]] | unique | if length == 1 then .[0] else empty end')"
  [[ "${running_image_id}" == "${desired_digest}" ]] || \
    fail "running web Pod digest ${running_image_id} does not match desired ${desired_digest}"

  http_response="$(curl --fail --silent --show-error --max-time 5 "http://${alb_hostname}/" 2>/dev/null || true)"
  grep -Fq 'CoffeeShop POS' <<<"${http_response}" || \
    fail "reconciled ALB endpoint did not return the CoffeeShop POS marker"
  verify_argocd_self_heal
  "${PROJECT_ROOT}/scripts/verify-gitops-health.sh" coffeeshop-prod
  "${PROJECT_ROOT}/scripts/verify-gitops-health.sh" coffeeshop-prod-platform
  echo "Runtime verification passed: EKS/nodes/controllers/GitOps healthy; ${healthy_count} ALB targets healthy across ${healthy_az_count} AZs."
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

wait_for_alb_deletion() {
  local load_balancer_arn="$1"
  local attempt

  [[ -n "${load_balancer_arn}" ]] || return
  for ((attempt = 1; attempt <= POLL_ATTEMPTS; attempt++)); do
    if ! aws elbv2 describe-load-balancers \
      --load-balancer-arns "${load_balancer_arn}" \
      >/dev/null 2>&1; then
      echo "Controller-owned ALB deletion confirmed."
      return
    fi
    echo "Attempt ${attempt}/${POLL_ATTEMPTS}: waiting for controller-owned ALB deletion... (10s)"
    sleep 10
  done
  fail "controller-owned ALB still exists; keep the controller running and diagnose finalizers/events"
}

tagged_resource_count() {
  local resource_type="$1"
  aws resourcegroupstaggingapi get-resources \
    --resource-type-filters "${resource_type}" \
    --tag-filters \
      "Key=Project,Values=${PROJECT_NAME}" \
      "Key=Environment,Values=${ENVIRONMENT}" \
    --query 'length(ResourceTagMappingList)' \
    --output text
}

verify_teardown_inventory() {
  local cluster_name="$1"
  local vpc_id="$2"
  local instance_count volume_count nat_count eip_count load_balancer_count target_group_count security_group_count log_group_count
  local rds_count secret_count
  local repository
  local retained_repositories=(
    go-coffeeshop-web
    go-coffeeshop-proxy
    go-coffeeshop-product
    go-coffeeshop-counter
    go-coffeeshop-barista
    go-coffeeshop-kitchen
    go-coffeeshop-migrate
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
  load_balancer_count="$(tagged_resource_count elasticloadbalancing:loadbalancer)"
  target_group_count="$(tagged_resource_count elasticloadbalancing:targetgroup)"
  security_group_count="$(tagged_resource_count ec2:security-group)"
  log_group_count="$(
    aws logs describe-log-groups \
      --query "length(logGroups[?starts_with(logGroupName, '/aws/eks/${cluster_name}/') || starts_with(logGroupName, '/aws/containerinsights/${cluster_name}/')])" \
      --output text
  )"
  rds_count="$(aws rds describe-db-instances \
    --query "length(DBInstances[?DBInstanceIdentifier=='${PROJECT_NAME}-${ENVIRONMENT}-db'])" \
    --output text)"
  secret_count="$(aws secretsmanager list-secrets \
    --include-planned-deletion \
    --query "length(SecretList[?Name=='/${PROJECT_NAME}/${ENVIRONMENT}/application'])" \
    --output text)"

  [[ "${instance_count}" -eq 0 ]] || fail "billable orphan inventory: ${instance_count} EC2 instance(s) remain"
  [[ "${volume_count}" -eq 0 ]] || fail "billable orphan inventory: ${volume_count} EBS volume(s) remain"
  [[ "${nat_count}" -eq 0 ]] || fail "billable orphan inventory: ${nat_count} NAT Gateway(s) remain"
  [[ "${eip_count}" -eq 0 ]] || fail "billable orphan inventory: ${eip_count} Elastic IP(s) remain"
  [[ "${load_balancer_count}" -eq 0 ]] || \
    fail "billable orphan inventory: ${load_balancer_count} tagged load balancer(s) remain"
  [[ "${target_group_count}" -eq 0 ]] || \
    fail "billable orphan inventory: ${target_group_count} tagged target group(s) remain"
  [[ "${security_group_count}" -eq 0 ]] || \
    fail "orphan inventory: ${security_group_count} tagged security group(s) remain"
  [[ "${log_group_count}" -eq 0 ]] || \
    fail "billable orphan inventory: ${log_group_count} EKS control-plane log group(s) remain"
  [[ "${rds_count}" -eq 0 ]] || fail "billable orphan inventory: ${rds_count} RDS instance(s) remain"
  [[ "${secret_count}" -eq 0 ]] || fail "orphan inventory: application secret still exists or is pending deletion"

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
  echo "Retained allowlist confirmed: backend S3/KMS, 8 ECR repositories/lifecycle policies, AWS Budget."
  echo "Cost Explorer is delayed; estimate-versus-actual remains pending until billing data arrives."
}

run_teardown() {
  local cluster_name cluster_arn vpc_id
  local plan_dir plan_file plan_json delete_count retained_delete_count unexpected_delete_count
  local destroy_targets=(
    -target=aws_db_instance.postgres
    -target=aws_db_subnet_group.rds
    -target=aws_security_group.rds
    -target=aws_secretsmanager_secret.coffeeshop_app_secret
    -target=aws_cloudwatch_log_group.application_logs
    -target=aws_cloudwatch_log_group.host_logs
    -target=aws_cloudwatch_log_group.dataplane_logs
    -target=aws_cloudwatch_metric_alarm.rds_free_storage
    -target=aws_cloudwatch_metric_alarm.node_cpu_high
    -target=aws_eks_addon.ebs_csi
    -target=aws_eks_addon.cloudwatch_observability
    -target=aws_eks_pod_identity_association.eso
    -target=aws_iam_role_policy_attachment.eso_attach
    -target=aws_iam_policy.eso_policy
    -target=aws_iam_role.eso_role
    -target=aws_eks_pod_identity_association.cloudwatch_agent
    -target=aws_iam_role_policy_attachment.cloudwatch_agent_attach
    -target=aws_iam_role.cloudwatch_agent_role
    -target=aws_eks_pod_identity_association.ebs_csi
    -target=aws_iam_role_policy_attachment.ebs_csi_attach
    -target=aws_iam_role.ebs_csi_role
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
            startswith("aws_db_instance.")
            or startswith("aws_db_subnet_group.")
            or startswith("aws_security_group.rds")
            or startswith("aws_secretsmanager_secret.")
            or startswith("aws_cloudwatch_log_group.")
            or startswith("aws_cloudwatch_metric_alarm.")
            or startswith("aws_eks_addon.")
            or startswith("aws_eks_pod_identity_association.")
            or startswith("aws_iam_role_policy_attachment.")
            or startswith("aws_iam_policy.")
            or startswith("aws_iam_role.")
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

  # PROD-2 ALB Ingress cleanup must complete while the controller is healthy.
  local alb_hostname="" alb_arn=""
  alb_hostname="$(kubectl get ingress coffeeshop-prod-alb-ingress \
    -n coffeeshop \
    -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)"

  if [[ -n "${alb_hostname}" ]]; then
    alb_arn="$(aws elbv2 describe-load-balancers \
      --query "LoadBalancers[?DNSName=='${alb_hostname}'].LoadBalancerArn | [0]" \
      --output text 2>/dev/null || true)"
    [[ "${alb_arn}" != "None" ]] || alb_arn=""
  fi
  if [[ -z "${alb_arn}" ]]; then
    alb_arn="$(aws resourcegroupstaggingapi get-resources \
      --resource-type-filters elasticloadbalancing:loadbalancer \
      --tag-filters \
        "Key=Project,Values=${PROJECT_NAME}" \
        "Key=Environment,Values=${ENVIRONMENT}" \
      --query 'ResourceTagMappingList[0].ResourceARN' \
      --output text 2>/dev/null || true)"
    [[ "${alb_arn}" != "None" ]] || alb_arn=""
  fi

  if kubectl get application coffeeshop-prod -n argocd >/dev/null 2>&1; then
    echo "Deleting Argo CD Application coffeeshop-prod while controller is healthy..."
    kubectl delete application coffeeshop-prod -n argocd --wait=true --timeout="${WAIT_TIMEOUT}"
  elif kubectl get ingress coffeeshop-prod-alb-ingress -n coffeeshop >/dev/null 2>&1; then
    echo "Argo CD Application is absent; deleting its ALB Ingress directly while the controller is healthy..."
    kubectl delete ingress coffeeshop-prod-alb-ingress \
      -n coffeeshop --wait=true --timeout="${WAIT_TIMEOUT}"
  elif [[ -n "${alb_arn}" ]]; then
    echo "Application and Ingress are already absent; resuming the wait for their tagged ALB deletion."
  fi
  if kubectl get application coffeeshop-prod-platform -n argocd >/dev/null 2>&1; then
    echo "Deleting Argo CD platform Application while controllers are healthy..."
    kubectl delete application coffeeshop-prod-platform \
      -n argocd --wait=true --timeout="${WAIT_TIMEOUT}"
  fi
  # A PROD-2-only cluster has never installed the PROD-3 ExternalSecret CRD.
  # --ignore-not-found covers a missing object, but not a missing resource type.
  if kubectl api-resources \
    --api-group=external-secrets.io \
    --output=name |
    grep -Fxq 'externalsecrets.external-secrets.io'; then
    kubectl delete externalsecrets.external-secrets.io \
      coffeeshop-rds-master-bootstrap \
      -n coffeeshop --ignore-not-found --wait=true --timeout="${WAIT_TIMEOUT}"
  else
    echo "ExternalSecret API is absent; skipping PROD-3 bootstrap secret cleanup."
  fi
  if [[ -n "${alb_arn}" ]]; then
    wait_for_alb_deletion "${alb_arn}"
  fi

  if helm status argocd -n argocd >/dev/null 2>&1; then
    echo "Uninstalling Argo CD Helm release..."
    helm uninstall argocd \
      --namespace argocd \
      --wait \
      --timeout "${WAIT_TIMEOUT}"
  fi

  if helm status aws-load-balancer-controller -n kube-system >/dev/null 2>&1; then
    echo "Uninstalling AWS Load Balancer Controller Helm release..."
    helm uninstall aws-load-balancer-controller \
      --namespace kube-system \
      --wait \
      --timeout "${WAIT_TIMEOUT}"
  fi

  # Do not replay the complete operator manifest during deletion. A PROD-2-only
  # cluster does not have cert-manager CRDs, so kubectl cannot map the optional
  # Certificate/Issuer documents even with --ignore-not-found. The operator is
  # namespace-scoped for this teardown boundary; cluster-scoped artifacts vanish
  # with the EKS cluster immediately afterwards.
  kubectl delete namespace rabbitmq-system \
    --ignore-not-found --wait=true --timeout="${WAIT_TIMEOUT}"
  if helm status external-secrets -n external-secrets >/dev/null 2>&1; then
    helm uninstall external-secrets -n external-secrets --wait --timeout "${WAIT_TIMEOUT}"
  fi
  if helm status cert-manager -n cert-manager >/dev/null 2>&1; then
    helm uninstall cert-manager -n cert-manager --wait --timeout "${WAIT_TIMEOUT}"
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
