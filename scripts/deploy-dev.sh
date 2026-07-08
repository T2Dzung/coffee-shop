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
  echo "Usage: $0 plan [terraform-plan-options] | apply" >&2
  exit 2
fi
shift

TF_PLAN_DIR="${TF_PLAN_DIR:-${HOME}/.cache/go-coffeeshop/terraform/plans}"
TF_PLAN_FILE="${TF_PLAN_DIR}/dev.tfplan"
install -d -m 0700 "${TF_PLAN_DIR}"

: "${ANSIBLE_PRIVATE_KEY_FILE:?Set ANSIBLE_PRIVATE_KEY_FILE to a key stored under the WSL filesystem}"
: "${AWS_DEFAULT_REGION:=ap-southeast-1}"
: "${ANSIBLE_VAULT_PASSWORD_FILE:=${HOME}/.vault-pass}"
export AWS_DEFAULT_REGION ANSIBLE_PRIVATE_KEY_FILE ANSIBLE_VAULT_PASSWORD_FILE

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

export PATH="${HOME}/.local/bin:${VENV_DIR}/bin:${PATH}"
for command_name in terraform aws ansible ansible-inventory ansible-playbook; do
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

case "${ACTION}" in
  plan)
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
    if [[ ! -f "${TF_PLAN_FILE}" ]]; then
      echo "Saved plan does not exist: ${TF_PLAN_FILE}. Run '$0 plan' first." >&2
      exit 1
    fi
    terraform -chdir="${DEV_TF_DIR}" apply "${TF_PLAN_FILE}"
    rm -f "${TF_PLAN_FILE}"
    ;;
  *)
    echo "Unknown action '${ACTION}'. Use 'plan' or 'apply'." >&2
    exit 2
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
  playbooks/site.yml

ansible-playbook \
  --inventory inventory/aws_ec2.yml \
  --private-key "${ANSIBLE_PRIVATE_KEY_FILE}" \
  playbooks/gitops_cicd.yml

KUBECONFIG="${HOME}/.kube/coffeeshop-dev.yaml" kubectl get nodes --output wide
