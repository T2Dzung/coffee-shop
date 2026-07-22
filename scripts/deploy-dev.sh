#!/usr/bin/env bash
set -Eeuo pipefail

# A single, retryable entry point replaces Terraform local-exec. Terraform owns
# AWS resources; Ansible owns host configuration. A failure in one layer can be
# rerun without corrupting the lifecycle of the other.

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEV_TF_DIR="${PROJECT_ROOT}/infrastructure/terraform/envs/dev"
ANSIBLE_DIR="${PROJECT_ROOT}/infrastructure/ansible"
VENV_DIR="${CONTROL_VENV_DIR:-${HOME}/.venvs/go-coffeeshop-platform}"
: "${TF_DATA_DIR:=${HOME}/.cache/go-coffeeshop/terraform/dev}"
export TF_DATA_DIR
install -d -m 0700 "${TF_DATA_DIR}"

ACTION="${1:-}"
if [[ -z "${ACTION}" ]]; then
  echo "Usage: $0 plan [terraform-plan-options] | apply | decommission-plan | decommission-apply" >&2
  exit 2
fi
shift

TF_PLAN_DIR="${TF_PLAN_DIR:-${HOME}/.cache/go-coffeeshop/terraform/plans}"
TF_PLAN_FILE="${TF_PLAN_DIR}/dev.tfplan"
TF_DECOMMISSION_PLAN_FILE="${TF_PLAN_DIR}/dev-decommission.tfplan"
install -d -m 0700 "${TF_PLAN_DIR}"

: "${AWS_DEFAULT_REGION:=ap-southeast-1}"
export AWS_DEFAULT_REGION

case "${ACTION}" in
  plan | apply)
    IS_DECOMMISSION=false
    ;;
  decommission-plan | decommission-apply)
    IS_DECOMMISSION=true
    ;;
  *)
    echo "Unknown action '${ACTION}'. Use plan, apply, decommission-plan, or decommission-apply." >&2
    exit 2
    ;;
esac

verify_decommission_account() {
  : "${DEV_EXPECTED_AWS_ACCOUNT_ID:?Set DEV_EXPECTED_AWS_ACCOUNT_ID before a DEV decommission action}"

  local actual_account_id
  actual_account_id="$(aws sts get-caller-identity --query Account --output text)"
  if [[ "${actual_account_id}" != "${DEV_EXPECTED_AWS_ACCOUNT_ID}" ]]; then
    echo "Refusing DEV decommission: caller account ${actual_account_id} does not match expected account ${DEV_EXPECTED_AWS_ACCOUNT_ID}." >&2
    exit 1
  fi
}

verify_retained_postgres_backup() {
  local backup_bucket backup_count
  backup_bucket="coffeeshop-dev-postgres-backup-${DEV_EXPECTED_AWS_ACCOUNT_ID}"
  backup_count="$(aws s3api list-objects-v2 \
    --bucket "${backup_bucket}" \
    --prefix coffeeshop-postgres/base/ \
    --max-keys 1 \
    --query KeyCount \
    --output text)"

  if [[ "${backup_count}" == "0" || "${backup_count}" == "None" ]]; then
    echo "Refusing DEV decommission: no PostgreSQL base-backup object was found in ${backup_bucket}." >&2
    exit 1
  fi
}

if [[ "${IS_DECOMMISSION}" == "false" ]]; then
  : "${ANSIBLE_PRIVATE_KEY_FILE:?Set ANSIBLE_PRIVATE_KEY_FILE to a key stored under the WSL filesystem}"
  : "${ANSIBLE_VAULT_PASSWORD_FILE:=${HOME}/.vault-pass}"
  export ANSIBLE_PRIVATE_KEY_FILE ANSIBLE_VAULT_PASSWORD_FILE

  export ANSIBLE_INVENTORY="${ANSIBLE_DIR}/inventory/aws_ec2.yml"
  export ANSIBLE_HOST_KEY_CHECKING=True
  export ANSIBLE_REMOTE_USER=ubuntu
  export ANSIBLE_FORKS=10
  export ANSIBLE_ROLES_PATH="${ANSIBLE_DIR}/roles"
  export ANSIBLE_INVENTORY_ENABLED="host_list,script,auto,yaml,ini,toml,amazon.aws.aws_ec2"
  export ANSIBLE_SSH_PIPELINING=True
  export ANSIBLE_SSH_ARGS="-o StrictHostKeyChecking=accept-new"

  if [[ "${ANSIBLE_PRIVATE_KEY_FILE}" == /mnt/* ]]; then
    echo "Keep the SSH private key under ~/.ssh in WSL, not on a /mnt filesystem." >&2
    exit 1
  fi

  if [[ ! -f "${ANSIBLE_PRIVATE_KEY_FILE}" ]]; then
    echo "SSH private key does not exist: ${ANSIBLE_PRIVATE_KEY_FILE}" >&2
    exit 1
  fi

  if [[ "$(stat -c '%a' "${ANSIBLE_PRIVATE_KEY_FILE}")" != "600" ]]; then
    echo "SSH private key must have mode 0600: ${ANSIBLE_PRIVATE_KEY_FILE}" >&2
    exit 1
  fi

  if [[ ! -x "${VENV_DIR}/bin/ansible-playbook" ]]; then
    echo "Run scripts/bootstrap-control-node.sh first." >&2
    exit 1
  fi
fi

export PATH="${HOME}/.local/bin:${VENV_DIR}/bin:${PATH}"
required_commands=(terraform aws)
if [[ "${IS_DECOMMISSION}" == "true" ]]; then
  required_commands+=(jq)
else
  required_commands+=(ansible ansible-inventory ansible-playbook)
fi

for command_name in "${required_commands[@]}"; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "Missing required command: ${command_name}" >&2
    exit 1
  }
done

# Fail before Terraform initialization if the control node has no valid AWS
# identity or the selected profile/session has expired.
aws sts get-caller-identity >/dev/null

terraform -chdir="${DEV_TF_DIR}" init
terraform -chdir="${DEV_TF_DIR}" validate -no-color
RUNTIME_ENABLED="$(printf 'var.dev_runtime_enabled\n' | terraform -chdir="${DEV_TF_DIR}" console -no-color)"

case "${ACTION}" in
  plan)
    if [[ "${RUNTIME_ENABLED}" != "true" ]]; then
      echo "DEV desired state is decommissioned. Use '$0 decommission-plan', or set dev_runtime_enabled=true for a rebuild plan." >&2
      exit 1
    fi
    terraform -chdir="${DEV_TF_DIR}" plan -out="${TF_PLAN_FILE}" "$@"
    chmod 0600 "${TF_PLAN_FILE}"
    terraform -chdir="${DEV_TF_DIR}" show -no-color "${TF_PLAN_FILE}"
    echo "Saved reviewed plan candidate: ${TF_PLAN_FILE}"
    echo "After reviewing it, run: $0 apply"
    exit 0
    ;;
  apply)
    if (($# > 0)); then
      echo "The apply action does not accept extra arguments; regenerate the saved plan instead." >&2
      exit 2
    fi
    if [[ "${RUNTIME_ENABLED}" != "true" ]]; then
      echo "Refusing normal apply while dev_runtime_enabled=false. Use decommission-apply for the guarded teardown plan." >&2
      exit 1
    fi
    if [[ ! -f "${TF_PLAN_FILE}" ]]; then
      echo "Saved plan does not exist: ${TF_PLAN_FILE}. Run '$0 plan' first." >&2
      exit 1
    fi
    terraform -chdir="${DEV_TF_DIR}" apply "${TF_PLAN_FILE}"
    rm -f "${TF_PLAN_FILE}"
    ;;
  decommission-plan)
    if (($# > 0)); then
      echo "The decommission-plan action does not accept extra Terraform arguments." >&2
      exit 2
    fi
    if [[ "${RUNTIME_ENABLED}" != "false" ]]; then
      echo "Refusing decommission plan: set dev_runtime_enabled=false in terraform.tfvars first." >&2
      exit 1
    fi

    verify_decommission_account
    verify_retained_postgres_backup
    terraform -chdir="${DEV_TF_DIR}" plan -out="${TF_DECOMMISSION_PLAN_FILE}"
    chmod 0600 "${TF_DECOMMISSION_PLAN_FILE}"

    DECOMMISSION_PLAN_JSON="$(mktemp)"
    trap 'rm -f "${DECOMMISSION_PLAN_JSON}"' EXIT
    terraform -chdir="${DEV_TF_DIR}" show -json "${TF_DECOMMISSION_PLAN_FILE}" >"${DECOMMISSION_PLAN_JSON}"

    if ! jq -e '
      [
        .resource_changes[]?
        | select(.change.actions != ["no-op"] and .change.actions != ["read"])
      ] as $mutations
      | ($mutations | length) > 0
      and all(
        $mutations[];
        .change.actions == ["delete"]
        and (
          .address
          | test(
              "^(aws_ebs_volume\\.longhorn|aws_volume_attachment\\.longhorn|aws_eip\\.haproxy|aws_eip_association\\.haproxy|aws_security_group\\.haproxy_sg|aws_iam_role\\.haproxy_role|aws_iam_role_policy_attachment\\.haproxy_ssm|aws_iam_instance_profile\\.haproxy_profile|aws_instance\\.haproxy|aws_vpc_security_group_ingress_rule\\.node_(api|http)_haproxy_ingress|aws_security_group\\.api_nlb_sg|aws_vpc_security_group_ingress_rule\\.(api_nlb_ingress(_all)?|node_api_nlb_ingress)|aws_vpc_security_group_egress_rule\\.api_nlb_egress|aws_lb\\.api_nlb|aws_lb_target_group\\.k3s_api|aws_lb_listener\\.k3s_api|aws_lb_target_group_attachment\\.k3s_api_nodes)\\[[0-9]+\\]$|^module\\.k3s_servers\\[[0-9]+\\]\\.module\\.ec2\\.aws_(instance|spot_instance_request)\\.[^.]+\\[[0-9]+\\]$"
            )
        )
      )
    ' "${DECOMMISSION_PLAN_JSON}" >/dev/null; then
      echo "Refusing decommission plan: it contains a non-delete action or a resource outside the runtime allowlist." >&2
      terraform -chdir="${DEV_TF_DIR}" show -no-color "${TF_DECOMMISSION_PLAN_FILE}" >&2
      rm -f "${TF_DECOMMISSION_PLAN_FILE}"
      exit 1
    fi

    echo "Validated DEV decommission mutation summary:"
    jq -r '
      .resource_changes[]?
      | select(.change.actions == ["delete"])
      | "  delete " + .address
    ' "${DECOMMISSION_PLAN_JSON}"
    terraform -chdir="${DEV_TF_DIR}" show -no-color "${TF_DECOMMISSION_PLAN_FILE}"
    echo "Saved guarded decommission plan: ${TF_DECOMMISSION_PLAN_FILE}"
    echo "After reviewing every delete, run: $0 decommission-apply"
    exit 0
    ;;
  decommission-apply)
    if (($# > 0)); then
      echo "The decommission-apply action does not accept extra arguments." >&2
      exit 2
    fi
    if [[ "${RUNTIME_ENABLED}" != "false" ]]; then
      echo "Refusing decommission apply: dev_runtime_enabled is no longer false." >&2
      exit 1
    fi
    if [[ ! -f "${TF_DECOMMISSION_PLAN_FILE}" ]]; then
      echo "Saved decommission plan does not exist: ${TF_DECOMMISSION_PLAN_FILE}. Run '$0 decommission-plan' first." >&2
      exit 1
    fi

    verify_decommission_account
    verify_retained_postgres_backup
    if [[ "${DEV_DECOMMISSION_ACKNOWLEDGE_DATA_LOSS:-}" != "DELETE-DEV-RUNTIME" ]]; then
      echo "Refusing decommission apply: set DEV_DECOMMISSION_ACKNOWLEDGE_DATA_LOSS=DELETE-DEV-RUNTIME after reviewing the saved plan." >&2
      exit 1
    fi
    terraform -chdir="${DEV_TF_DIR}" apply "${TF_DECOMMISSION_PLAN_FILE}"
    rm -f "${TF_DECOMMISSION_PLAN_FILE}"
    echo "DEV runtime decommission apply completed. Run scripts/verify-dev-decommission.sh next."
    exit 0
    ;;
esac

echo "Waiting for AWS instance status checks..."
INSTANCE_IDS=$(aws ec2 describe-instances \
  --filters \
    "Name=tag:Environment,Values=dev" \
    "Name=tag:ManagedBy,Values=Terraform" \
    "Name=instance-state-name,Values=running" \
  --query "Reservations[*].Instances[*].InstanceId" \
  --output text)

if [[ -n "${INSTANCE_IDS}" ]]; then
  # shellcheck disable=SC2086
  aws ec2 wait instance-status-ok --instance-ids ${INSTANCE_IDS}
fi

cd "${ANSIBLE_DIR}"

for attempt in $(seq 1 30); do
  # shellcheck disable=SC2016
  if ansible all --module-name ansible.builtin.raw --args 'cloud-init status --wait; STATUS=$?; [ $STATUS -eq 0 ] || [ $STATUS -eq 2 ]' \
      --private-key "${ANSIBLE_PRIVATE_KEY_FILE}"; then
    break
  fi
  if [[ "${attempt}" == "30" ]]; then
    echo "Instances did not become SSH/cloud-init ready within the timeout." >&2
    exit 1
  fi
  sleep 10
done

ansible-playbook --inventory "localhost," --syntax-check playbooks/site.yml
API_ENDPOINT_PROVIDER=$(terraform -chdir="${DEV_TF_DIR}" output -raw active_api_endpoint_provider)
ACTIVE_API_ENDPOINT=$(terraform -chdir="${DEV_TF_DIR}" output -raw active_api_endpoint)
K3S_REGISTRATION_ENDPOINT=$(terraform -chdir="${DEV_TF_DIR}" output -raw k3s_registration_endpoint)
K3S_TLS_SANS=$(terraform -chdir="${DEV_TF_DIR}" output -json k3s_tls_sans)
LONGHORN_DATA_VOLUME_SIZE=$(terraform -chdir="${DEV_TF_DIR}" output -raw longhorn_data_volume_size)

POSTGRES_BACKUP_BUCKET=$(terraform -chdir="${DEV_TF_DIR}" output -raw postgres_backup_bucket_name)
POSTGRES_BACKUP_ACCESS_KEY=$(terraform -chdir="${DEV_TF_DIR}" output -raw postgres_backup_iam_access_key_id)
POSTGRES_BACKUP_SECRET_KEY=$(terraform -chdir="${DEV_TF_DIR}" output -raw postgres_backup_iam_secret_access_key)
export POSTGRES_BACKUP_BUCKET_NAME="${POSTGRES_BACKUP_BUCKET}"
export POSTGRES_BACKUP_IAM_ACCESS_KEY_ID="${POSTGRES_BACKUP_ACCESS_KEY}"
export POSTGRES_BACKUP_IAM_SECRET_ACCESS_KEY="${POSTGRES_BACKUP_SECRET_KEY}"

ansible-playbook \
  --inventory inventory/aws_ec2.yml \
  --private-key "${ANSIBLE_PRIVATE_KEY_FILE}" \
  --extra-vars "active_api_endpoint_provider=${API_ENDPOINT_PROVIDER}" \
  --extra-vars "active_api_endpoint=${ACTIVE_API_ENDPOINT}" \
  --extra-vars "k3s_registration_endpoint=${K3S_REGISTRATION_ENDPOINT}" \
  --extra-vars "{\"k3s_tls_sans\": ${K3S_TLS_SANS}}" \
  --extra-vars "longhorn_prereqs_data_volume_size=${LONGHORN_DATA_VOLUME_SIZE}" \
  playbooks/site.yml

ansible-playbook \
  --inventory inventory/aws_ec2.yml \
  --private-key "${ANSIBLE_PRIVATE_KEY_FILE}" \
  playbooks/gitops_cicd.yml

KUBECONFIG="${HOME}/.kube/coffeeshop-dev.yaml" kubectl get nodes --output wide
