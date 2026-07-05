#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ANSIBLE_DIR="${PROJECT_ROOT}/infrastructure/ansible"
DEV_TF_DIR="${PROJECT_ROOT}/infrastructure/terraform/envs/dev"
K8S_DIR="${PROJECT_ROOT}/infrastructure/k8s"
SCHEMA_VERSION="1.35.4"
: "${TF_DATA_DIR:=${HOME}/.cache/go-coffeeshop/terraform/dev}"
export TF_DATA_DIR
install -d -m 0700 "${TF_DATA_DIR}"

# Keep lint ignore paths deterministic regardless of the caller's directory.
cd "${PROJECT_ROOT}"
export ANSIBLE_CONFIG="${ANSIBLE_DIR}/ansible.cfg"
export ANSIBLE_ROLES_PATH="${ANSIBLE_DIR}/roles"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

for command_name in terraform ansible-playbook ansible-lint yamllint kubeconform kubectl shellcheck; do
  require_command "${command_name}"
done

terraform -chdir="${PROJECT_ROOT}/infrastructure/terraform" fmt -check -recursive
terraform -chdir="${DEV_TF_DIR}" validate -no-color

ansible-playbook --inventory "localhost," --syntax-check "${ANSIBLE_DIR}/playbooks/site.yml"
ansible-playbook --inventory "localhost," --syntax-check "${ANSIBLE_DIR}/playbooks/post_start.yml"
ansible-playbook --inventory "localhost," --syntax-check "${ANSIBLE_DIR}/playbooks/backup_baseline.yml"
ansible-playbook --inventory "localhost," --syntax-check "${ANSIBLE_DIR}/playbooks/gitops_cicd.yml"
(
  cd "${ANSIBLE_DIR}"
  # Explicit globs prevent a false pass where ansible-lint treats the current
  # directory as one candidate but discovers no playbook or role task files.
  ansible-lint playbooks/*.yml roles/*/tasks/*.yml
)

yamllint --config-file "${PROJECT_ROOT}/.yamllint.yml" \
  "${ANSIBLE_DIR}" "${K8S_DIR}"

mapfile -d '' manifest_files < <(
  find "${K8S_DIR}/gateway" "${K8S_DIR}/policies" "${K8S_DIR}/gitops" \
    -type f \( -name '*.yaml' -o -name '*.yml' \) \
    ! -name '*values.yaml' \
    -print0
  printf '%s\0' "${ANSIBLE_DIR}/roles/nfs_shared/templates/cache-pvc.yaml.j2"
)
kubeconform \
  -kubernetes-version "${SCHEMA_VERSION}" \
  -ignore-missing-schemas \
  -strict \
  -summary \
  "${manifest_files[@]}"

kubectl kustomize "${K8S_DIR}/gitops/apps/coffeeshop/overlays/dev" | kubeconform \
  -kubernetes-version "${SCHEMA_VERSION}" \
  -ignore-missing-schemas \
  -strict \
  -summary

shellcheck "${PROJECT_ROOT}/scripts/"*.sh

if ! grep -Fxq '/infrastructure/k8s/gitops/apps/coffeeshop/base/secrets.yaml' "${PROJECT_ROOT}/.gitignore"; then
  echo "The runtime Kubernetes secret manifest is not ignored." >&2
  exit 1
fi

echo "Infrastructure validation completed successfully."
