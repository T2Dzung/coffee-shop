#!/usr/bin/env bash
set -Eeuo pipefail

region="${1:?AWS Region is required}"
secret_id="${2:?application secret ARN is required}"
rds_endpoint="${3:?RDS endpoint is required}"
secret_file=""

cleanup() {
  [[ -z "${secret_file}" ]] || rm -f -- "${secret_file}"
}
trap cleanup EXIT

for command_name in aws jq openssl; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "Missing required command: ${command_name}" >&2
    exit 1
  }
done

current_secret="$(aws secretsmanager get-secret-value \
  --region "${region}" \
  --secret-id "${secret_id}" \
  --query SecretString \
  --output text 2>/dev/null || true)"
if jq -e '
  (.APP_DB_PASSWORD | type == "string" and length >= 32)
  and (.PG_URL | test("^postgres://coffeeshop_app:"))
  and (.RABBITMQ_DEFAULT_PASS | type == "string" and length >= 32)
' >/dev/null 2>&1 <<<"${current_secret}"; then
  echo "Application/broker secret is already seeded; preserving the existing credential version."
  exit 0
fi

app_password="$(openssl rand -hex 24)"
rabbitmq_password="$(openssl rand -hex 24)"
pg_url="postgres://coffeeshop_app:${app_password}@${rds_endpoint}/postgres?sslmode=require"
rabbitmq_url="amqp://coffeeshop:${rabbitmq_password}@coffeeshop-rabbitmq.coffeeshop.svc.cluster.local:5672/"

secret_file="$(mktemp)"
chmod 0600 "${secret_file}"
jq -n \
  --arg app_password "${app_password}" \
  --arg pg_url "${pg_url}" \
  --arg rabbitmq_password "${rabbitmq_password}" \
  --arg rabbitmq_url "${rabbitmq_url}" \
  '{
    APP_DB_PASSWORD: $app_password,
    PG_URL: $pg_url,
    PG_DSN_URL: $pg_url,
    RABBITMQ_DEFAULT_USER: "coffeeshop",
    RABBITMQ_DEFAULT_PASS: $rabbitmq_password,
    RABBITMQ_DEFAULT_USER_CONFIG:
      ("default_user = coffeeshop\ndefault_pass = " + $rabbitmq_password + "\n"),
    RABBITMQ_URL: $rabbitmq_url
  }' >"${secret_file}"

aws secretsmanager put-secret-value \
  --region "${region}" \
  --secret-id "${secret_id}" \
  --secret-string "file://${secret_file}" \
  --query VersionId \
  --output text >/dev/null
echo "Application/broker secret seeded without printing credential material."
