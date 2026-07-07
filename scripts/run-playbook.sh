#!/usr/bin/env bash
# Ansible Playbook Runner Utility for WSL/Windows Mount Environment
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ANSIBLE_DIR="${PROJECT_ROOT}/infrastructure/ansible"
KEY_SOURCE="/mnt/c/Users/My Computer/Downloads/coffeeshop-secrets/coffeeshop-runner-key.pem"
KEY_WSL="${HOME}/.ssh/coffeeshop-runner-key.pem"

# Validate that the playbook argument is provided
if [ $# -lt 1 ]; then
  echo "Usage: $0 <playbook-path> [ansible-playbook-arguments...]" >&2
  echo "Example: $0 playbooks/gitops_cicd.yml --tags monitoring_secrets" >&2
  exit 1
fi

PLAYBOOK_ARG="$1"
shift

# 1. Resolve absolute path of playbook if needed
PLAYBOOK_PATH="${ANSIBLE_DIR}/${PLAYBOOK_ARG}"
if [ ! -f "${PLAYBOOK_PATH}" ] && [ -f "${PLAYBOOK_ARG}" ]; then
  PLAYBOOK_PATH="$(realpath "${PLAYBOOK_ARG}")"
fi

if [ ! -f "${PLAYBOOK_PATH}" ]; then
  echo "Error: Playbook file not found at '${PLAYBOOK_PATH}'" >&2
  exit 1
fi

# 2. Automatically copy SSH key to secure WSL environment if not present
if [ ! -f "${KEY_WSL}" ]; then
  if [ -f "${KEY_SOURCE}" ]; then
    echo "Copying SSH private key to secure WSL directory (~/.ssh/)..."
    mkdir -p "${HOME}/.ssh" && chmod 700 "${HOME}/.ssh"
    cp "${KEY_SOURCE}" "${KEY_WSL}"
    chmod 600 "${KEY_WSL}"
  else
    echo "Warning: SSH private key not found at source path:" >&2
    echo "  ${KEY_SOURCE}" >&2
    echo "Ensure your private key exists or is managed by SSH-agent." >&2
  fi
fi

# 3. Export WSL-safe Ansible Configurations
export ANSIBLE_ROLES_PATH="${ANSIBLE_DIR}/roles"
export ANSIBLE_INVENTORY_ENABLED="host_list,script,auto,yaml,ini,toml,amazon.aws.aws_ec2"
export ANSIBLE_HOST_KEY_CHECKING=False

# 4. Check for vault password file helper
VAULT_PASS_FILE="${HOME}/.vault-pass"
EXTRA_ARGS=()
if [ -f "${VAULT_PASS_FILE}" ]; then
  EXTRA_ARGS+=("--vault-password-file" "${VAULT_PASS_FILE}")
fi

# 5. Connect connection parameters if key exists
if [ -f "${KEY_WSL}" ]; then
  export ANSIBLE_PRIVATE_KEY_FILE="${KEY_WSL}"
  EXTRA_ARGS+=("--private-key" "${KEY_WSL}")
fi

# 6. Execute playbook
cd "${ANSIBLE_DIR}"
echo "--------------------------------------------------------"
echo "Starting Ansible Execution..."
echo "Playbook: ${PLAYBOOK_PATH}"
echo "Extra Args: ${EXTRA_ARGS[*]} $*"
echo "--------------------------------------------------------"

ansible-playbook \
  -i inventory/aws_ec2.yml \
  -u ubuntu \
  "${EXTRA_ARGS[@]}" \
  "$@" \
  "${PLAYBOOK_PATH}"
