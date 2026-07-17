#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ANSIBLE_DIR="${PROJECT_ROOT}/infrastructure/ansible"
DEV_TF_DIR="${PROJECT_ROOT}/infrastructure/terraform/envs/dev"
K8S_DIR="${PROJECT_ROOT}/infrastructure/k8s"
SCHEMA_VERSION="1.35.4"
VENV_DIR="${CONTROL_VENV_DIR:-${HOME}/.venvs/go-coffeeshop-platform}"
KUBECONFORM_CACHE_DIR="${KUBECONFORM_CACHE_DIR:-${HOME}/.cache/go-coffeeshop/kubeconform}"
KUBECONFORM_CONCURRENCY="${KUBECONFORM_CONCURRENCY:-2}"
: "${TF_DATA_DIR:=${HOME}/.cache/go-coffeeshop/terraform/dev}"
export TF_DATA_DIR
install -d -m 0700 "${TF_DATA_DIR}" "${KUBECONFORM_CACHE_DIR}"
export PATH="${HOME}/.local/bin:${VENV_DIR}/bin:${PATH}"

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

for command_name in terraform ansible-playbook ansible-lint yamllint kubeconform kubectl shellcheck helm; do
  require_command "${command_name}"
done

KUBECONFORM_COMMON_ARGS=(
  -kubernetes-version "${SCHEMA_VERSION}"
  -cache "${KUBECONFORM_CACHE_DIR}"
  -n "${KUBECONFORM_CONCURRENCY}"
  -ignore-missing-schemas
  # Operator-managed CRDs depend on remote CRD schemas that are flaky in local WSL.
  # Validate YAML/rendering locally and rely on operator runtime reconciliation for CR shape.
  -skip "PrometheusRule,RabbitmqCluster,Cluster,ObjectStore,ScheduledBackup,Issuer"
  -schema-location 'default'
  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
  -strict
  -summary
)

terraform -chdir="${PROJECT_ROOT}/infrastructure/terraform" fmt -check -recursive
terraform -chdir="${DEV_TF_DIR}" validate -no-color

ansible-playbook --inventory "localhost," --syntax-check "${ANSIBLE_DIR}/playbooks/site.yml"
ansible-playbook --inventory "localhost," --syntax-check "${ANSIBLE_DIR}/playbooks/post_start.yml"
ansible-playbook --inventory "localhost," --syntax-check "${ANSIBLE_DIR}/playbooks/backup_baseline.yml"
ansible-playbook --inventory "localhost," --syntax-check "${ANSIBLE_DIR}/playbooks/gitops_cicd.yml"
ansible-playbook --inventory "localhost," --syntax-check "${ANSIBLE_DIR}/playbooks/phase4_cleanup_nfs.yml"
(
  cd "${ANSIBLE_DIR}"
  # Explicit globs prevent a false pass where ansible-lint treats the current
  # directory as one candidate but discovers no playbook or role task files.
  ansible-lint playbooks/*.yml roles/*/tasks/*.yml
)

yamllint --config-file "${PROJECT_ROOT}/.yamllint.yml" \
  "${ANSIBLE_DIR}" "${K8S_DIR}"

mapfile -d '' manifest_files < <(
  find "${K8S_DIR}/bootstrap" "${K8S_DIR}/gateway" "${K8S_DIR}/policies" "${K8S_DIR}/gitops" \
    -type f \( -name '*.yaml' -o -name '*.yml' \) \
    ! -name '*values.yaml' \
    ! -name 'Chart.yaml' \
    ! -name 'kustomization.yaml' \
    ! -name 'kustomization.yml' \
    ! -path '*/templates/*' \
    -print0
)
kubeconform \
  "${KUBECONFORM_COMMON_ARGS[@]}" \
  "${manifest_files[@]}"

kubectl kustomize "${K8S_DIR}/gitops/apps/coffeeshop/overlays/dev" | kubeconform \
  "${KUBECONFORM_COMMON_ARGS[@]}"

kubectl kustomize "${K8S_DIR}/gitops/addons/arc/hardening" | kubeconform \
  "${KUBECONFORM_COMMON_ARGS[@]}"

kubectl kustomize "${K8S_DIR}/gitops/addons/monitoring-rules" | kubeconform \
  "${KUBECONFORM_COMMON_ARGS[@]}"

# Validate local coffeeshop-rabbitmq helm chart template rendering early, before
# version consistency checks, so manifest/schema issues fail close to the source.
if [ -d "${K8S_DIR}/gitops/apps/coffeeshop-rabbitmq" ]; then
  # RabbitmqCluster is a CRD and kubeconform may fail on external schema
  # downloads before falling back to the CRD catalog. Validate built-in
  # resources here; the CRD is installed and reconciled by the operator app.
  helm template "${K8S_DIR}/gitops/apps/coffeeshop-rabbitmq" | kubeconform \
    "${KUBECONFORM_COMMON_ARGS[@]}"
fi

shellcheck "${PROJECT_ROOT}/scripts/"*.sh

# Terraform is the deployment source of truth. Keep the Ansible standalone
# default aligned so a direct site.yml recovery cannot validate the wrong disk size.
TF_LONGHORN_SIZE=$(awk '
  /variable "longhorn_data_volume_size"/ { in_block=1 }
  in_block && $1 == "default" { print $3; exit }
  in_block && /^}/ { in_block=0 }
' "${DEV_TF_DIR}/variables.tf")
ANSIBLE_LONGHORN_SIZE=$(awk '$1 == "longhorn_prereqs_data_volume_size:" { print $2 }' \
  "${ANSIBLE_DIR}/roles/longhorn_prereqs/defaults/main.yml")

if [ "${TF_LONGHORN_SIZE}" != "${ANSIBLE_LONGHORN_SIZE}" ]; then
  echo "Error: Longhorn data volume size mismatch between Terraform and Ansible." >&2
  echo "Terraform: '${TF_LONGHORN_SIZE}' vs Ansible: '${ANSIBLE_LONGHORN_SIZE}'" >&2
  exit 1
fi

if ! grep -Fxq '/infrastructure/k8s/gitops/apps/coffeeshop/base/secrets.yaml' "${PROJECT_ROOT}/.gitignore"; then
  echo "The runtime Kubernetes secret manifest is not ignored." >&2
  exit 1
fi

if find "${K8S_DIR}/gitops" -maxdepth 1 -type f -name 'root-app.y*ml' | grep -q .; then
  echo "The root application must stay outside the directory it manages." >&2
  exit 1
fi

# Validate version consistency for kube-prometheus-stack between Ansible and GitOps
VERSION_IN_ANSIBLE=$(grep 'kube_prometheus_stack_chart_version:' "${ANSIBLE_DIR}/playbooks/group_vars/all/versions.yml" | awk '{print $2}' | tr -d '"')
VERSION_IN_GITOPS=$(grep 'chart: kube-prometheus-stack' -A 1 "${K8S_DIR}/gitops/monitoring-app.yaml" | grep 'targetRevision:' | awk '{print $2}')

if [ "${VERSION_IN_ANSIBLE}" != "${VERSION_IN_GITOPS}" ]; then
  echo "Error: Version mismatch for kube-prometheus-stack chart." >&2
  echo "Ansible version: '${VERSION_IN_ANSIBLE}' vs GitOps targetRevision: '${VERSION_IN_GITOPS}'" >&2
  exit 1
fi

# Validate version consistency for longhorn between Ansible and GitOps
LONGHORN_VERSION_IN_ANSIBLE=$(grep 'longhorn_chart_version:' "${ANSIBLE_DIR}/playbooks/group_vars/all/versions.yml" | awk '{print $2}' | tr -d '"')
LONGHORN_VERSION_IN_GITOPS=$(grep 'chart: longhorn' -A 1 "${K8S_DIR}/gitops/longhorn-app.yaml" | grep 'targetRevision:' | awk '{print $2}')

if [ "${LONGHORN_VERSION_IN_ANSIBLE}" != "${LONGHORN_VERSION_IN_GITOPS}" ]; then
  echo "Error: Version mismatch for longhorn chart." >&2
  echo "Ansible version: '${LONGHORN_VERSION_IN_ANSIBLE}' vs GitOps targetRevision: '${LONGHORN_VERSION_IN_GITOPS}'" >&2
  exit 1
fi

# Validate version consistency for the Loki backend chart.
LOKI_VERSION_IN_ANSIBLE=$(grep 'loki_chart_version:' "${ANSIBLE_DIR}/playbooks/group_vars/all/versions.yml" | awk '{print $2}' | tr -d '"')
LOKI_VERSION_IN_GITOPS=$(grep 'chart: loki' -A 1 "${K8S_DIR}/gitops/loki-app.yaml" | grep 'targetRevision:' | awk '{print $2}')

if [ "${LOKI_VERSION_IN_ANSIBLE}" != "${LOKI_VERSION_IN_GITOPS}" ]; then
  echo "Error: Version mismatch for Loki chart." >&2
  echo "Ansible version: '${LOKI_VERSION_IN_ANSIBLE}' vs GitOps targetRevision: '${LOKI_VERSION_IN_GITOPS}'" >&2
  exit 1
fi

# Render the pinned remote chart with repository-owned values. This catches
# chart schema/topology regressions that validating the Argo Application alone
# cannot see. Network/schema failures must be reported as external blockers,
# never treated as a manifest pass.
helm template loki loki \
  --repo https://grafana-community.github.io/helm-charts \
  --version "${LOKI_VERSION_IN_ANSIBLE}" \
  --namespace observability \
  --kube-version "${SCHEMA_VERSION}" \
  --values "${K8S_DIR}/gitops/addons/loki/values.yaml" | kubeconform \
  "${KUBECONFORM_COMMON_ARGS[@]}"

# Validate version consistency and the rendered least-privilege contract for
# the node-local Alloy log collector.
ALLOY_VERSION_IN_ANSIBLE=$(grep 'alloy_chart_version:' "${ANSIBLE_DIR}/playbooks/group_vars/all/versions.yml" | awk '{print $2}' | tr -d '"')
ALLOY_VERSION_IN_GITOPS=$(grep 'chart: alloy' -A 1 "${K8S_DIR}/gitops/alloy-app.yaml" | grep 'targetRevision:' | awk '{print $2}')
ALLOY_APP_VERSION_IN_ANSIBLE=$(grep 'alloy_app_version:' "${ANSIBLE_DIR}/playbooks/group_vars/all/versions.yml" | awk '{print $2}' | tr -d '"')

if [ "${ALLOY_VERSION_IN_ANSIBLE}" != "${ALLOY_VERSION_IN_GITOPS}" ]; then
  echo "Error: Version mismatch for Alloy chart." >&2
  echo "Ansible version: '${ALLOY_VERSION_IN_ANSIBLE}' vs GitOps targetRevision: '${ALLOY_VERSION_IN_GITOPS}'" >&2
  exit 1
fi

ALLOY_RENDERED=$(mktemp --suffix=.yaml)
trap 'rm -f "${ALLOY_RENDERED}"' EXIT

helm template alloy alloy \
  --repo https://grafana.github.io/helm-charts \
  --version "${ALLOY_VERSION_IN_ANSIBLE}" \
  --namespace observability \
  --kube-version "${SCHEMA_VERSION}" \
  --values "${K8S_DIR}/gitops/addons/alloy/values.yaml" >"${ALLOY_RENDERED}"

kubeconform "${KUBECONFORM_COMMON_ARGS[@]}" "${ALLOY_RENDERED}"

if ! grep -q '^kind: DaemonSet$' "${ALLOY_RENDERED}"; then
  echo "Error: Alloy must render as a DaemonSet." >&2
  exit 1
fi

if ! grep -Fq "image: docker.io/grafana/alloy:${ALLOY_APP_VERSION_IN_ANSIBLE}" "${ALLOY_RENDERED}"; then
  echo "Error: Rendered Alloy image does not match the app version contract." >&2
  exit 1
fi

if ! grep -Fq 'runAsUser: 0' "${ALLOY_RENDERED}" ||
  grep -Fq 'privileged: true' "${ALLOY_RENDERED}" ||
  ! grep -Fq 'readOnlyRootFilesystem: true' "${ALLOY_RENDERED}"; then
  echo "Error: Alloy host-log reader security boundary is not preserved." >&2
  exit 1
fi

if grep -Eq '^[[:space:]]+- (secrets|configmaps)$' "${ALLOY_RENDERED}"; then
  echo "Error: Alloy RBAC must not read Secrets or ConfigMaps." >&2
  exit 1
fi

if ! grep -Fq 'regex  = "cluster|namespace|workload|pod|container|service_name|level"' "${ALLOY_RENDERED}"; then
  echo "Error: Alloy bounded Loki label allowlist is missing." >&2
  exit 1
fi

if ! grep -Fq 'path: /var/lib/alloy/coffeeshop-positions' "${ALLOY_RENDERED}" ||
  ! grep -Fq 'type: DirectoryOrCreate' "${ALLOY_RENDERED}"; then
  echo "Error: Alloy positions must persist in the dedicated node-local hostPath." >&2
  exit 1
fi

if grep -F -B 4 'name: alloy-storage' "${ALLOY_RENDERED}" |
  grep -Fq 'emptyDir:'; then
  echo "Error: Alloy positions storage must not use Pod-local emptyDir." >&2
  exit 1
fi

if ! grep -Fq 'tail_from_end = false' "${ALLOY_RENDERED}"; then
  echo "Error: Alloy must read new CRI files from the beginning after positions migration." >&2
  exit 1
fi

if ! grep -F -A 3 'file_match {' "${ALLOY_RENDERED}" |
  grep -Fq 'enabled     = true'; then
  echo "Error: Alloy must expand the globbed CRI log paths with file_match." >&2
  exit 1
fi

# Validate version consistency for cloudnative-pg between Ansible and GitOps.
# CNPG uses separate Helm chart and operator app versions; ArgoCD must pin the chart version.
CNPG_VERSION_IN_ANSIBLE=$(grep 'cloudnativepg_chart_version:' "${ANSIBLE_DIR}/playbooks/group_vars/all/versions.yml" | awk '{print $2}' | tr -d '"')
CNPG_VERSION_IN_GITOPS=$(grep 'chart: cloudnative-pg' -A 1 "${K8S_DIR}/gitops/cloudnativepg-app.yaml" | grep 'targetRevision:' | awk '{print $2}')

if [ "${CNPG_VERSION_IN_ANSIBLE}" != "${CNPG_VERSION_IN_GITOPS}" ]; then
  echo "Error: Version mismatch for cloudnative-pg chart." >&2
  echo "Ansible chart version: '${CNPG_VERSION_IN_ANSIBLE}' vs GitOps targetRevision: '${CNPG_VERSION_IN_GITOPS}'" >&2
  exit 1
fi

# Validate version consistency for Barman Cloud Plugin between Ansible and GitOps.
BARMAN_VERSION_IN_ANSIBLE=$(grep 'barman_cloud_plugin_chart_version:' "${ANSIBLE_DIR}/playbooks/group_vars/all/versions.yml" | awk '{print $2}' | tr -d '"')
BARMAN_VERSION_IN_GITOPS=$(grep 'chart: plugin-barman-cloud' -A 1 "${K8S_DIR}/gitops/barman-cloud-plugin-app.yaml" | grep 'targetRevision:' | awk '{print $2}')

if [ "${BARMAN_VERSION_IN_ANSIBLE}" != "${BARMAN_VERSION_IN_GITOPS}" ]; then
  echo "Error: Version mismatch for Barman Cloud Plugin chart." >&2
  echo "Ansible chart version: '${BARMAN_VERSION_IN_ANSIBLE}' vs GitOps targetRevision: '${BARMAN_VERSION_IN_GITOPS}'" >&2
  exit 1
fi

# Validate version consistency for vendored RabbitMQ Cluster Operator manifest.
RABBITMQ_VERSION_IN_ANSIBLE=$(grep 'rabbitmq_cluster_operator_version:' "${ANSIBLE_DIR}/playbooks/group_vars/all/versions.yml" | awk '{print $2}' | tr -d '"v')
RABBITMQ_VERSION_IN_MANIFEST=$(grep 'image: ghcr.io/rabbitmq/cluster-operator:' "${K8S_DIR}/gitops/addons/rabbitmq-operator/cluster-operator.yaml" | head -1 | sed 's/.*cluster-operator://' | tr -d '\r')

if [ "${RABBITMQ_VERSION_IN_ANSIBLE}" != "${RABBITMQ_VERSION_IN_MANIFEST}" ]; then
  echo "Error: Version mismatch for RabbitMQ Cluster Operator." >&2
  echo "Ansible version: '${RABBITMQ_VERSION_IN_ANSIBLE}' vs vendored manifest image tag: '${RABBITMQ_VERSION_IN_MANIFEST}'" >&2
  exit 1
fi

# Validate local coffeeshop-postgres helm chart template rendering.
# The production chart intentionally requires backup.bucketName, which is supplied
# by Ansible from Terraform output when creating the ArgoCD Application.
if [ -d "${K8S_DIR}/gitops/apps/coffeeshop-postgres" ]; then
  helm template "${K8S_DIR}/gitops/apps/coffeeshop-postgres" \
    --set backup.bucketName=coffeeshop-static-validation-bucket | kubeconform \
    "${KUBECONFORM_COMMON_ARGS[@]}"
fi

echo "Infrastructure validation completed successfully."
