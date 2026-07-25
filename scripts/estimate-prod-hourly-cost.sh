#!/usr/bin/env bash
set -Eeuo pipefail

REGION="${1:?AWS Region is required}"
INSTANCE_TYPE="${2:?node instance type is required}"
NODE_COUNT="${3:?node count is required}"
DISK_GIB_PER_NODE="${4:?disk GiB per node is required}"
ALB_COUNT="${5:-0}"
PUBLIC_IPV4_COUNT="${6:-1}"
RDS_INSTANCE_CLASS="${7:-db.t4g.micro}"
RDS_STORAGE_GIB="${8:-20}"
RABBITMQ_EBS_GIB="${9:-15}"
PRICING_REGION="us-east-1"
HOURS_PER_MONTH="730"

for numeric_input in NODE_COUNT DISK_GIB_PER_NODE ALB_COUNT PUBLIC_IPV4_COUNT RDS_STORAGE_GIB RABBITMQ_EBS_GIB; do
  [[ "${!numeric_input}" =~ ^[0-9]+$ ]] || {
    echo "${numeric_input} must be a non-negative integer" >&2
    exit 1
  }
done

for command_name in aws jq awk; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "Missing required command: ${command_name}" >&2
    exit 1
  }
done

price_per_unit() {
  local service_code="$1"
  shift
  local response price
  response="$(aws pricing get-products \
    --region "${PRICING_REGION}" \
    --service-code "${service_code}" \
    --filters "$@" \
    --max-results 20 \
    --output json)"
  price="$(jq -r '
    [.PriceList[] | fromjson | .terms.OnDemand[]?.priceDimensions[]?.pricePerUnit.USD]
    | map(select(. != null)) | first // empty
  ' <<<"${response}")"
  [[ -n "${price}" ]] || {
    echo "No current On-Demand price found for ${service_code}: $*" >&2
    exit 1
  }
  printf '%s' "${price}"
}

price_per_unit_with_unit() {
  local service_code="$1"
  local billing_unit="$2"
  shift 2
  local response price
  response="$(aws pricing get-products \
    --region "${PRICING_REGION}" \
    --service-code "${service_code}" \
    --filters "$@" \
    --max-results 20 \
    --output json)"
  price="$(jq -r --arg unit "${billing_unit}" '
    [.PriceList[] | fromjson | .terms.OnDemand[]?.priceDimensions[]?
      | select(.unit == $unit) | .pricePerUnit.USD]
    | map(select(. != null)) | first // empty
  ' <<<"${response}")"
  [[ -n "${price}" ]] || {
    echo "No current ${billing_unit} On-Demand price found for ${service_code}: $*" >&2
    exit 1
  }
  printf '%s' "${price}"
}

region_filter="Type=TERM_MATCH,Field=regionCode,Value=${REGION}"

eks_rate="$(price_per_unit AmazonEKS \
  "${region_filter}" \
  'Type=TERM_MATCH,Field=locationType,Value=AWS Region' \
  'Type=TERM_MATCH,Field=tiertype,Value=HAStandard')"

instance_rate="$(price_per_unit AmazonEC2 \
  "${region_filter}" \
  "Type=TERM_MATCH,Field=instanceType,Value=${INSTANCE_TYPE}" \
  'Type=TERM_MATCH,Field=operatingSystem,Value=Linux' \
  'Type=TERM_MATCH,Field=tenancy,Value=Shared' \
  'Type=TERM_MATCH,Field=preInstalledSw,Value=NA' \
  'Type=TERM_MATCH,Field=capacitystatus,Value=Used')"

nat_rate="$(price_per_unit AmazonEC2 \
  "${region_filter}" \
  'Type=TERM_MATCH,Field=productFamily,Value=NAT Gateway')"

gp3_gib_month_rate="$(price_per_unit AmazonEC2 \
  "${region_filter}" \
  'Type=TERM_MATCH,Field=productFamily,Value=Storage' \
  'Type=TERM_MATCH,Field=volumeApiName,Value=gp3')"

ipv4_rate="$(price_per_unit AmazonVPC \
  "${region_filter}" \
  'Type=TERM_MATCH,Field=group,Value=VPCPublicIPv4Address' \
  'Type=TERM_MATCH,Field=groupDescription,Value=Hourly charge for In-use Public IPv4 Addresses')"

rds_instance_rate="$(price_per_unit_with_unit AmazonRDS Hrs \
  "${region_filter}" \
  'Type=TERM_MATCH,Field=productFamily,Value=Database Instance' \
  "Type=TERM_MATCH,Field=instanceType,Value=${RDS_INSTANCE_CLASS}" \
  'Type=TERM_MATCH,Field=databaseEngine,Value=PostgreSQL' \
  'Type=TERM_MATCH,Field=deploymentOption,Value=Single-AZ')"

rds_storage_gib_month_rate="$(price_per_unit_with_unit AmazonRDS GB-Mo \
  "${region_filter}" \
  'Type=TERM_MATCH,Field=productFamily,Value=Database Storage' \
  'Type=TERM_MATCH,Field=databaseEngine,Value=PostgreSQL' \
  'Type=TERM_MATCH,Field=deploymentOption,Value=Single-AZ' \
  'Type=TERM_MATCH,Field=volumeType,Value=General Purpose-GP3')"

alb_rate="0"
if ((ALB_COUNT > 0)); then
  alb_rate="$(price_per_unit_with_unit AWSELB Hrs \
    "${region_filter}" \
    'Type=TERM_MATCH,Field=productFamily,Value=Load Balancer-Application' \
    'Type=TERM_MATCH,Field=operation,Value=LoadBalancing:Application')"
fi

node_total="$(awk -v rate="${instance_rate}" -v count="${NODE_COUNT}" 'BEGIN { printf "%.6f", rate * count }')"
ebs_total="$(awk -v rate="${gp3_gib_month_rate}" -v node_gib="${DISK_GIB_PER_NODE}" \
  -v node_count="${NODE_COUNT}" -v rmq_gib="${RABBITMQ_EBS_GIB}" -v hours="${HOURS_PER_MONTH}" \
  'BEGIN { printf "%.6f", rate * ((node_gib * node_count) + rmq_gib) / hours }')"
rds_storage_total="$(awk -v rate="${rds_storage_gib_month_rate}" -v gib="${RDS_STORAGE_GIB}" \
  -v hours="${HOURS_PER_MONTH}" 'BEGIN { printf "%.6f", rate * gib / hours }')"
alb_total="$(awk -v rate="${alb_rate}" -v count="${ALB_COUNT}" 'BEGIN { printf "%.6f", rate * count }')"
ipv4_total="$(awk -v rate="${ipv4_rate}" -v count="${PUBLIC_IPV4_COUNT}" \
  'BEGIN { printf "%.6f", rate * count }')"
rds_total="$(awk -v rate="${rds_instance_rate}" 'BEGIN { printf "%.6f", rate }')"

fixed_total="$(awk -v eks="${eks_rate}" -v nodes="${node_total}" -v nat="${nat_rate}" \
  -v ebs="${ebs_total}" -v rds_storage="${rds_storage_total}" -v alb="${alb_total}" \
  -v ipv4="${ipv4_total}" -v rds="${rds_total}" \
  'BEGIN { printf "%.4f", eks + nodes + nat + ebs + rds_storage + alb + ipv4 + rds }')"

cat <<EOF
Estimated fixed cost for 1 hour (${REGION}, current AWS On-Demand rates):
  EKS standard control plane : USD $(printf '%.4f' "${eks_rate}")
  ${NODE_COUNT} x ${INSTANCE_TYPE} nodes       : USD $(printf '%.4f' "${node_total}")
  1 x NAT Gateway           : USD $(printf '%.4f' "${nat_rate}")
  1 x RDS ${RDS_INSTANCE_CLASS}       : USD $(printf '%.4f' "${rds_total}")
  Node + RabbitMQ gp3 EBS : USD $(printf '%.4f' "${ebs_total}")
  ${RDS_STORAGE_GIB} GiB RDS gp3 storage : USD $(printf '%.4f' "${rds_storage_total}")
  ${ALB_COUNT} x Application Load Balancer : USD $(printf '%.4f' "${alb_total}")
  ${PUBLIC_IPV4_COUNT} x public IPv4          : USD $(printf '%.4f' "${ipv4_total}")
  ------------------------------------------------------------
  Fixed hourly estimate     : USD ${fixed_total}

Usage-priced LCU, traffic, NAT GB processing, CloudWatch log ingestion,
API requests and taxes are not knowable before runtime and are not included.
Price source: AWS Price List API, queried at $(date -u +%Y-%m-%dT%H:%M:%SZ).
EOF
