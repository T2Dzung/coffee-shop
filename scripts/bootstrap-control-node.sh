#!/usr/bin/env bash
set -Eeuo pipefail

# Bootstrap the canonical WSL2 Ubuntu 24.04 control node. Tools are installed
# under the user's home so the project never depends on mutable PATHs.

TERRAFORM_VERSION="1.15.7"
KUBECTL_VERSION="v1.35.4"
HELM_VERSION="v3.21.0"
KUBECONFORM_VERSION="v0.8.0"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${HOME}/.local/bin"
VENV_DIR="${CONTROL_VENV_DIR:-${HOME}/.venvs/go-coffeeshop-platform}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

require_ubuntu_2404() {
  # shellcheck disable=SC1091
  source /etc/os-release
  if [[ "${ID}" != "ubuntu" || "${VERSION_ID}" != "24.04" ]]; then
    echo "Ubuntu 24.04 is required; found ${PRETTY_NAME}." >&2
    echo "Install it with: wsl --install -d Ubuntu-24.04" >&2
    exit 1
  fi
}

download() {
  local url="$1"
  local output="$2"
  curl --fail --location --retry 3 --silent --show-error "${url}" --output "${output}"
}

verify_sha256() {
  local file="$1"
  local expected="$2"
  echo "${expected}  ${file}" | sha256sum --check --status
}

install_terraform() {
  local archive="${TMP_DIR}/terraform.zip"
  local sums="${TMP_DIR}/terraform_SHA256SUMS"
  download "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_amd64.zip" "${archive}"
  download "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_SHA256SUMS" "${sums}"
  local expected
  expected="$(awk -v name="terraform_${TERRAFORM_VERSION}_linux_amd64.zip" '$2 == name {print $1}' "${sums}")"
  verify_sha256 "${archive}" "${expected}"
  unzip -oq "${archive}" -d "${TMP_DIR}/terraform"
  install -m 0755 "${TMP_DIR}/terraform/terraform" "${BIN_DIR}/terraform"
}

install_kubectl() {
  local binary="${TMP_DIR}/kubectl"
  download "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" "${binary}"
  local expected
  expected="$(curl --fail --location --retry 3 --silent --show-error "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl.sha256")"
  verify_sha256 "${binary}" "${expected}"
  install -m 0755 "${binary}" "${BIN_DIR}/kubectl"
}

install_helm() {
  local archive="${TMP_DIR}/helm.tar.gz"
  download "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" "${archive}"
  local expected
  expected="$(curl --fail --location --retry 3 --silent --show-error "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz.sha256sum" | awk '{print $1}')"
  verify_sha256 "${archive}" "${expected}"
  tar -xzf "${archive}" -C "${TMP_DIR}"
  install -m 0755 "${TMP_DIR}/linux-amd64/helm" "${BIN_DIR}/helm"
}

install_kubeconform() {
  local archive="${TMP_DIR}/kubeconform.tar.gz"
  download "https://github.com/yannh/kubeconform/releases/download/${KUBECONFORM_VERSION}/kubeconform-linux-amd64.tar.gz" "${archive}"
  tar -xzf "${archive}" -C "${TMP_DIR}" kubeconform
  install -m 0755 "${TMP_DIR}/kubeconform" "${BIN_DIR}/kubeconform"
}

require_ubuntu_2404
sudo apt-get update
sudo apt-get install -y ca-certificates curl diffutils git jq openssh-client python3-pip python3-venv shellcheck unzip
mkdir -p "${BIN_DIR}"

python3 -m venv "${VENV_DIR}"
"${VENV_DIR}/bin/python" -m pip install --upgrade pip
"${VENV_DIR}/bin/pip" install --requirement "${PROJECT_ROOT}/infrastructure/ansible/requirements-controller.txt"
"${VENV_DIR}/bin/ansible-galaxy" collection install --requirements-file "${PROJECT_ROOT}/infrastructure/ansible/requirements.yml"

install_terraform
install_kubectl
install_helm
install_kubeconform

if ! command -v aws >/dev/null 2>&1; then
  echo "AWS CLI v2 is still required. Install it from the official AWS installer, then configure ~/.aws." >&2
fi

cat <<EOF
Control-node bootstrap completed.
Add this line to ~/.bashrc if it is not already present:
  export PATH="${BIN_DIR}:\$PATH"
Activate the project environment with:
  source "${VENV_DIR}/bin/activate"
EOF
