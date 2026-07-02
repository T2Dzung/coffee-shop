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

: "${ANSIBLE_PRIVATE_KEY_FILE:?Set ANSIBLE_PRIVATE_KEY_FILE to a key stored under the WSL filesystem}"
: "${AWS_DEFAULT_REGION:=ap-southeast-1}"
export AWS_DEFAULT_REGION ANSIBLE_PRIVATE_KEY_FILE

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
terraform -chdir="${DEV_TF_DIR}" apply "$@"

echo "Waiting for AWS instance status checks..."
aws ec2 wait instance-status-ok \
  --filters \
    "Name=tag:Environment,Values=dev" \
    "Name=tag:ManagedBy,Values=Terraform" \
    "Name=instance-state-name,Values=running"

cd "${ANSIBLE_DIR}"

for attempt in $(seq 1 30); do
  if ansible all --module-name ansible.builtin.raw --args 'cloud-init status --wait' \
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
ansible-playbook \
  --inventory inventory/aws_ec2.yml \
  --private-key "${ANSIBLE_PRIVATE_KEY_FILE}" \
  playbooks/site.yml

KUBECONFIG="${DEV_TF_DIR}/dev-kubeconfig" kubectl get nodes --output wide
