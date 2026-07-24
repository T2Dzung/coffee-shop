#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ANSIBLE_DIR="${PROJECT_ROOT}/infrastructure/ansible"
DEV_KUBECONFIG="${PROJECT_ROOT}/infrastructure/terraform/envs/dev/dev-kubeconfig"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_ROOT="${BASELINE_BACKUP_DESTINATION:-${HOME}/coffeeshop-backups/phase0/${TIMESTAMP}}"

: "${ANSIBLE_PRIVATE_KEY_FILE:?Set ANSIBLE_PRIVATE_KEY_FILE to the WSL SSH private key}"
export BASELINE_BACKUP_DESTINATION="${BACKUP_ROOT}"
export KUBECONFIG="${KUBECONFIG:-${DEV_KUBECONFIG}}"

install -d -m 0700 "${BACKUP_ROOT}/etcd" "${BACKUP_ROOT}/postgres" "${BACKUP_ROOT}/rabbitmq"

cd "${ANSIBLE_DIR}"
ansible-playbook \
  --inventory inventory/aws_ec2.yml \
  --private-key "${ANSIBLE_PRIVATE_KEY_FILE}" \
  playbooks/backup_baseline.yml

kubectl --namespace coffeeshop exec deployment/postgres -- \
  pg_dump --username postgres --format custom postgres \
  >"${BACKUP_ROOT}/postgres/postgres.dump"

rabbitmq_pod="$(kubectl --namespace coffeeshop get pod \
  --selector app=rabbitmq --output jsonpath='{.items[0].metadata.name}')"
kubectl --namespace coffeeshop exec "${rabbitmq_pod}" -- \
  rabbitmqctl export_definitions /tmp/rabbitmq-definitions.json
kubectl --namespace coffeeshop cp \
  "${rabbitmq_pod}:/tmp/rabbitmq-definitions.json" \
  "${BACKUP_ROOT}/rabbitmq/definitions.json"
kubectl --namespace coffeeshop exec "${rabbitmq_pod}" -- \
  rm -f /tmp/rabbitmq-definitions.json

snapshot_file="$(find "${BACKUP_ROOT}/etcd" -maxdepth 1 -type f -print -quit)"
test -n "${snapshot_file}" && test -s "${snapshot_file}"
test -s "${BACKUP_ROOT}/postgres/postgres.dump"
test -s "${BACKUP_ROOT}/rabbitmq/definitions.json"

find "${BACKUP_ROOT}" -type f -print0 \
  | sort -z \
  | xargs -0 sha256sum \
  >"${BACKUP_ROOT}/SHA256SUMS"
chmod -R go-rwx "${BACKUP_ROOT}"

echo "Baseline backup completed: ${BACKUP_ROOT}"
