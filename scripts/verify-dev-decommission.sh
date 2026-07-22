#!/usr/bin/env bash
set -Eeuo pipefail

: "${DEV_EXPECTED_AWS_ACCOUNT_ID:?Set DEV_EXPECTED_AWS_ACCOUNT_ID before verifying DEV decommission}"
: "${AWS_DEFAULT_REGION:=ap-southeast-1}"
export AWS_DEFAULT_REGION

command -v aws >/dev/null 2>&1 || {
  echo "Missing required command: aws" >&2
  exit 1
}

require_zero() {
  local count_name="$1"
  local count_value="$2"

  if [[ "${count_value}" != "0" ]]; then
    echo "DEV decommission verification failed: ${count_name}=${count_value}, expected 0." >&2
    exit 1
  fi
}

actual_account_id="$(aws sts get-caller-identity --query Account --output text)"
if [[ "${actual_account_id}" != "${DEV_EXPECTED_AWS_ACCOUNT_ID}" ]]; then
  echo "Refusing verification: caller account ${actual_account_id} does not match expected account ${DEV_EXPECTED_AWS_ACCOUNT_ID}." >&2
  exit 1
fi

instance_count="$(aws ec2 describe-instances \
  --filters \
    "Name=tag:Environment,Values=dev" \
    "Name=tag:ManagedBy,Values=Terraform" \
    "Name=instance-state-name,Values=pending,running,stopping,stopped,shutting-down" \
  --query 'length(Reservations[].Instances[])' \
  --output text)"

volume_count="$(aws ec2 describe-volumes \
  --filters \
    "Name=tag:Environment,Values=dev" \
    "Name=tag:ManagedBy,Values=Terraform" \
  --query 'length(Volumes)' \
  --output text)"

eip_count="$(aws ec2 describe-addresses \
  --filters \
    "Name=tag:Environment,Values=dev" \
    "Name=tag:ManagedBy,Values=Terraform" \
  --query 'length(Addresses)' \
  --output text)"

load_balancer_count="$(aws resourcegroupstaggingapi get-resources \
  --resource-type-filters elasticloadbalancing:loadbalancer \
  --tag-filters Key=Environment,Values=dev Key=ManagedBy,Values=Terraform \
  --query 'length(ResourceTagMappingList)' \
  --output text)"

require_zero "instance_count" "${instance_count}"
require_zero "volume_count" "${volume_count}"
require_zero "eip_count" "${eip_count}"
require_zero "load_balancer_count" "${load_balancer_count}"

state_bucket="coffeeshop-terraform-state-${DEV_EXPECTED_AWS_ACCOUNT_ID}"
backup_bucket="coffeeshop-dev-postgres-backup-${DEV_EXPECTED_AWS_ACCOUNT_ID}"

aws s3api head-object \
  --bucket "${state_bucket}" \
  --key dev/terraform.tfstate \
  >/dev/null

backup_count="$(aws s3api list-objects-v2 \
  --bucket "${backup_bucket}" \
  --prefix coffeeshop-postgres/base/ \
  --max-keys 1 \
  --query 'KeyCount' \
  --output text)"

if [[ "${backup_count}" == "0" || "${backup_count}" == "None" ]]; then
  echo "DEV decommission verification failed: no PostgreSQL base-backup object was found in ${backup_bucket}." >&2
  exit 1
fi

echo "DEV decommission verification passed."
echo "  billable runtime instances: 0"
echo "  DEV EBS volumes: 0"
echo "  DEV Elastic IPs: 0"
echo "  DEV load balancers: 0"
echo "  retained Terraform state: s3://${state_bucket}/dev/terraform.tfstate"
echo "  retained PostgreSQL backups: s3://${backup_bucket}/coffeeshop-postgres/base/"
