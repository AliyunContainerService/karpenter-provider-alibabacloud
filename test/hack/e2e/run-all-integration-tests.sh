#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  CLUSTER_ID=<ack-cluster-id> ALIYUN_PROFILE=<aliyun-cli-profile> \
    test/hack/e2e/run-all-integration-tests.sh

Alternatively, provide ALIBABA_CLOUD_ACCESS_KEY_ID and
ALIBABA_CLOUD_ACCESS_KEY_SECRET instead of ALIYUN_PROFILE.

Optional variables:
  RESULT_DIR                      Output directory
  KUBECONFIG_OUT                  Kubeconfig path
  ALIYUN_REGION_HINT              Bootstrap region for explicit AK/SK authentication
  DRY_RUN=1                       Discover without running tests
  SKIP_CLUSTER_CONNECTIVITY_CHECK=1  Skip the initial kubectl connectivity check
  ALLOW_CONCURRENT_E2E=true       Allow another integration test process
  SKIP_KARPENTER_READY_CHECK=1    Skip deployment readiness check
  EXTRA_LABEL_FILTER              Additional Ginkgo label expression
  DISCOVERY_SYSTEM_DISK_CATEGORY  Default: cloud_essd
USAGE
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
CLUSTER_ID="${CLUSTER_ID:-}"

if [[ -z "${CLUSTER_ID}" ]]; then
  usage >&2
  exit 2
fi

for command in aliyun jq kubectl go; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    printf 'required command not found: %s\n' "${command}" >&2
    exit 2
  fi
done

umask 077
RESULT_DIR="${RESULT_DIR:-${REPO_ROOT}/integration-results-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "${RESULT_DIR}"
RESULT_DIR="$(cd "${RESULT_DIR}" && pwd)"
ALL_LOG="${RESULT_DIR}/all.log"
SUMMARY="${RESULT_DIR}/summary.tsv"
DISCOVERED_ENV="${RESULT_DIR}/discovered.env"
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/karpenter-e2e.XXXXXX")"
: >"${ALL_LOG}"
printf 'suite\tresult\texit_code\tduration_seconds\n' >"${SUMMARY}"

cleanup() {
  rm -rf "${TEMP_DIR}"
}
trap cleanup EXIT

log() {
  printf '%s\n' "$*" | tee -a "${ALL_LOG}"
}

fail() {
  log "ERROR: $*"
  exit 1
}

ALIYUN_AUTH_ARGS=()
if [[ -n "${ALIYUN_PROFILE:-}" ]]; then
  PROFILE_JSON="$(aliyun configure get --profile "${ALIYUN_PROFILE}")" || fail "unable to read ALIYUN_PROFILE=${ALIYUN_PROFILE}"
  ALIBABA_CLOUD_ACCESS_KEY_ID="$(jq -er '.access_key_id | select(type == "string" and length > 0)' <<<"${PROFILE_JSON}")" || fail "ALIYUN_PROFILE must expose an AK access_key_id for the Go integration tests"
  ALIBABA_CLOUD_ACCESS_KEY_SECRET="$(jq -er '.access_key_secret | select(type == "string" and length > 0)' <<<"${PROFILE_JSON}")" || fail "ALIYUN_PROFILE must expose an AK access_key_secret for the Go integration tests"
  export ALIBABA_CLOUD_ACCESS_KEY_ID ALIBABA_CLOUD_ACCESS_KEY_SECRET
  ALIYUN_AUTH_ARGS=(--profile "${ALIYUN_PROFILE}")
elif [[ -n "${ALIBABA_CLOUD_ACCESS_KEY_ID:-}" && -n "${ALIBABA_CLOUD_ACCESS_KEY_SECRET:-}" ]]; then
  TEMP_PROFILE="karpenter-e2e"
  TEMP_CONFIG="${TEMP_DIR}/aliyun-config.json"
  BOOTSTRAP_REGION="${ALIYUN_REGION_HINT:-$(aliyun configure get 2>/dev/null | jq -r '.region_id // empty')}"
  BOOTSTRAP_REGION="${BOOTSTRAP_REGION:-cn-hangzhou}"
  jq -n \
    --arg id "${ALIBABA_CLOUD_ACCESS_KEY_ID}" \
    --arg secret "${ALIBABA_CLOUD_ACCESS_KEY_SECRET}" \
    --arg region "${BOOTSTRAP_REGION}" \
    '{current: "karpenter-e2e", meta_path: "", profiles: [{name: "karpenter-e2e", mode: "AK", access_key_id: $id, access_key_secret: $secret, region_id: $region, language: "en", output_format: "json"}]}' \
    >"${TEMP_CONFIG}"
  ALIYUN_AUTH_ARGS=(--profile "${TEMP_PROFILE}" --config-path "${TEMP_CONFIG}")
else
  fail "set ALIYUN_PROFILE or both ALIBABA_CLOUD_ACCESS_KEY_ID and ALIBABA_CLOUD_ACCESS_KEY_SECRET"
fi

aliyun_cmd() {
  aliyun "$@" "${ALIYUN_AUTH_ARGS[@]}"
}

CLUSTER_JSON="${TEMP_DIR}/cluster.json"
log "Discovering ACK cluster ${CLUSTER_ID}"
aliyun_cmd cs GET "/clusters/${CLUSTER_ID}" >"${CLUSTER_JSON}" 2>>"${ALL_LOG}" || fail "unable to describe ACK cluster"

TEST_CLUSTER_NAME="$(jq -er '.name | select(type == "string" and length > 0)' "${CLUSTER_JSON}")" || fail "cluster name was not returned"
TEST_CLUSTER_ID="${CLUSTER_ID}"
TEST_REGION="$(jq -er '(.region_id // .parameters["ALIYUN::Region"]) | select(type == "string" and length > 0)' "${CLUSTER_JSON}")" || fail "cluster region was not returned"
CLUSTER_IP_STACK="$(jq -r '(.ip_stack // .parameters.IPStack // "ipv4") | ascii_downcase' "${CLUSTER_JSON}")"
TEST_RAM_ROLE="$(jq -r '.worker_ram_role_name // empty' "${CLUSTER_JSON}")"
KUBECONFIG="${KUBECONFIG_OUT:-${HOME}/.kube/config-${CLUSTER_ID}}"
export TEST_CLUSTER_NAME TEST_CLUSTER_ID TEST_REGION TEST_RAM_ROLE KUBECONFIG

refresh_kubeconfig() {
  local response="${TEMP_DIR}/kubeconfig.json"
  mkdir -p "$(dirname "${KUBECONFIG}")"
  aliyun_cmd cs GET "/k8s/${CLUSTER_ID}/user_config" \
    --PrivateIpAddress false --region "${TEST_REGION}" >"${response}" 2>>"${ALL_LOG}" || return 1
  jq -er '.config | select(type == "string" and length > 0)' "${response}" >"${KUBECONFIG}" || return 1
  chmod 600 "${KUBECONFIG}"
}

refresh_kubeconfig || fail "unable to obtain the public kubeconfig"
TEST_CLUSTER_ENDPOINT="$(kubectl --kubeconfig "${KUBECONFIG}" config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
[[ -n "${TEST_CLUSTER_ENDPOINT}" ]] || fail "cluster endpoint was not found in kubeconfig"
export TEST_CLUSTER_ENDPOINT

if [[ "${SKIP_CLUSTER_CONNECTIVITY_CHECK:-0}" != "1" ]]; then
  if ! kubectl --kubeconfig "${KUBECONFIG}" get namespace --request-timeout=15s >/dev/null 2>>"${ALL_LOG}"; then
    fail "kubectl cannot reach ${TEST_CLUSTER_ENDPOINT}; configure the required network route and rerun"
  fi
fi

WORKERS_JSON="${TEMP_DIR}/workers.json"
aliyun_cmd ecs DescribeInstances --RegionId "${TEST_REGION}" \
  --Tag.1.Key ack.aliyun.com --Tag.1.Value "${CLUSTER_ID}" --PageSize 100 >"${WORKERS_JSON}" 2>>"${ALL_LOG}" || fail "unable to discover ACK worker instances"

VSWITCH_IDS_JSON="$(jq -c '
  def ids:
    if type == "array" then .[]
    elif type == "string" and startswith("[") then fromjson[]?
    elif type == "string" then split(",")[]
    else empty
    end;
  [
    ((.vswitch_ids // []) | ids),
    (.outputs[]? | select(.OutputKey == "VSwitchIds") | .OutputValue | ids),
    ((.parameters.WorkerVSwitchIds // "") | ids),
    (.vswitch_id // empty)
  ]
  | map(select(type == "string" and length > 0))
  | unique
' "${CLUSTER_JSON}")"
[[ "$(jq 'length' <<<"${VSWITCH_IDS_JSON}")" -gt 0 ]] || fail "no VSwitch IDs were discoverable from the cluster"

VSWITCH_NDJSON="${TEMP_DIR}/vswitches.ndjson"
: >"${VSWITCH_NDJSON}"
while IFS= read -r vswitch_id; do
  response="${TEMP_DIR}/vswitch-${vswitch_id}.json"
  aliyun_cmd vpc DescribeVSwitches --RegionId "${TEST_REGION}" --VSwitchId "${vswitch_id}" >"${response}" 2>>"${ALL_LOG}" || fail "unable to describe VSwitch ${vswitch_id}"
  jq -c '.VSwitches.VSwitch[]? | {id: .VSwitchId, zone: .ZoneId, ipv6CIDR: (.Ipv6CidrBlock // "")}' "${response}" >>"${VSWITCH_NDJSON}"
done < <(jq -r '.[]' <<<"${VSWITCH_IDS_JSON}")

VSWITCH_DATA="$(jq -sc 'map(select(.id != null and .zone != null)) | unique_by(.id)' "${VSWITCH_NDJSON}")"
[[ "$(jq 'length' <<<"${VSWITCH_DATA}")" -gt 0 ]] || fail "VSwitch metadata was empty"
TEST_VSWITCH_IDS="$(jq -r 'map(.id) | join(",")' <<<"${VSWITCH_DATA}")"
TEST_ZONES="$(jq -r 'map(.zone) | unique | join(",")' <<<"${VSWITCH_DATA}")"
export TEST_VSWITCH_IDS TEST_ZONES

SECURITY_GROUP_IDS_JSON="$(jq -nc --slurpfile cluster "${CLUSTER_JSON}" --slurpfile workers "${WORKERS_JSON}" '
  [
    ($cluster[0].security_group_ids[]?),
    ($cluster[0].security_group_id // empty),
    ($cluster[0].parameters.SecurityGroupId // empty),
    ($workers[0].Instances.Instance[]?.SecurityGroupIds.SecurityGroupId[]?)
  ]
  | map(select(type == "string" and length > 0))
  | unique
')"
[[ "$(jq 'length' <<<"${SECURITY_GROUP_IDS_JSON}")" -gt 0 ]] || fail "no security group IDs were discoverable"
TEST_SECURITY_GROUP_IDS="$(jq -r 'join(",")' <<<"${SECURITY_GROUP_IDS_JSON}")"
TEST_SECURITY_GROUP_ID="$(jq -r '.[0]' <<<"${SECURITY_GROUP_IDS_JSON}")"
export TEST_SECURITY_GROUP_IDS TEST_SECURITY_GROUP_ID

TEST_IMAGE_ID="$(jq -r '
  [
    (.Instances.Instance[]? | select(.Status == "Running") | .ImageId),
    (.Instances.Instance[]?.ImageId)
  ]
  | map(select(type == "string" and length > 0))
  | first // empty
' "${WORKERS_JSON}")"
if [[ -z "${TEST_IMAGE_ID}" ]]; then
  TEST_IMAGE_ID="$(jq -r '[.parameters.WorkerImageId, .parameters.ImageId] | map(select(type == "string" and length > 0)) | first // empty' "${CLUSTER_JSON}")"
fi
[[ -n "${TEST_IMAGE_ID}" ]] || fail "no worker image ID was discoverable"
export TEST_IMAGE_ID

ZONES_JSON="$(jq -c 'map(.zone) | unique' <<<"${VSWITCH_DATA}")"
ZONE_COUNT="$(jq 'length' <<<"${ZONES_JSON}")"
AVAILABLE_JSON="${TEMP_DIR}/available-resources.json"
DISCOVERY_SYSTEM_DISK_CATEGORY="${DISCOVERY_SYSTEM_DISK_CATEGORY:-cloud_essd}"
aliyun_cmd ecs DescribeAvailableResource --RegionId "${TEST_REGION}" \
  --DestinationResource InstanceType --ResourceType instance --InstanceChargeType PostPaid \
  --SystemDiskCategory "${DISCOVERY_SYSTEM_DISK_CATEGORY}" --NetworkCategory vpc >"${AVAILABLE_JSON}" 2>>"${ALL_LOG}" || fail "unable to discover ECS instance stock"

STOCK_JSON="${TEMP_DIR}/stock.json"
jq -c --argjson zones "${ZONES_JSON}" '
  [
    .AvailableZones.AvailableZone[]?
    | select(.ZoneId as $zone | $zones | index($zone))
    | .ZoneId as $zone
    | .AvailableResources.AvailableResource[]?
    | select(.Type == "InstanceType")
    | .SupportedResources.SupportedResource[]?
    | select(.Status == "Available" and .StatusCategory == "WithStock")
    | {id: .Value, zone: $zone}
  ]
' "${AVAILABLE_JSON}" >"${STOCK_JSON}"
[[ "$(jq 'length' "${STOCK_JSON}")" -gt 0 ]] || fail "no in-stock ECS instance types were found in cluster zones"

INSTANCE_TYPES_NDJSON="${TEMP_DIR}/instance-types.ndjson"
INSTANCE_TYPES_JSON="${TEMP_DIR}/instance-types.json"
: >"${INSTANCE_TYPES_NDJSON}"
next_token=""
page=0
while :; do
  page=$((page + 1))
  [[ "${page}" -le 100 ]] || fail "DescribeInstanceTypes pagination exceeded 100 pages"
  response="${TEMP_DIR}/instance-types-${page}.json"
  instance_type_args=(ecs DescribeInstanceTypes --MaxResults 100)
  if [[ -n "${next_token}" ]]; then
    instance_type_args+=(--NextToken "${next_token}")
  fi
  aliyun_cmd "${instance_type_args[@]}" >"${response}" 2>>"${ALL_LOG}" || fail "unable to describe ECS instance type metadata"
  jq -c '.InstanceTypes.InstanceType[]?' "${response}" >>"${INSTANCE_TYPES_NDJSON}"
  next_token="$(jq -r '.NextToken // empty' "${response}")"
  [[ -n "${next_token}" ]] || break
done
jq -s '.' "${INSTANCE_TYPES_NDJSON}" >"${INSTANCE_TYPES_JSON}"

TEST_INSTANCE_TYPES="$(jq -nr \
  --slurpfile workers "${WORKERS_JSON}" \
  --slurpfile stock "${STOCK_JSON}" \
  --slurpfile metadata "${INSTANCE_TYPES_JSON}" \
  --argjson zoneCount "${ZONE_COUNT}" '
    ($stock[0] | group_by(.id) | map({id: .[0].id, zones: ([.[].zone] | unique)})) as $stockByType
    | ($stockByType | map(select((.zones | length) == $zoneCount)) | map(.id)) as $commonStock
    | (if ($commonStock | length) >= 2 then $commonStock else ($stockByType | map(.id)) end) as $allowed
    | ([$workers[0].Instances.Instance[]? | select((.GPUAmount // 0) == 0) | .InstanceType] | unique) as $existing
    | ([$metadata[0][]
        | select((.GPUAmount // 0) == 0)
        | select((.CpuArchitecture // "") == "X86")
        | select((.CpuCoreCount // 0) >= 4 and (.MemorySize // 0) >= 8)
        | select(.EniTrunkSupported == true)
        | {id: .InstanceTypeId, cpu: .CpuCoreCount, memory: .MemorySize}]
       | sort_by(.cpu, .memory, .id)
       | map(.id)) as $eligible
    | reduce (($existing + $eligible)[]) as $id ([]; if index($id) then . else . + [$id] end)
    | map(select(. as $id | $allowed | index($id)))
    | .[0:2]
    | join(",")
  ')"
[[ -n "${TEST_INSTANCE_TYPES}" ]] || fail "no suitable ordinary ECS instance type was discoverable"
export TEST_INSTANCE_TYPES

TEST_GPU_INSTANCE_TYPES="$(jq -nr \
  --slurpfile stock "${STOCK_JSON}" \
  --slurpfile metadata "${INSTANCE_TYPES_JSON}" '
    ($stock[0] | map(.id) | unique) as $available
    | [$metadata[0][]
       | select((.GPUAmount // 0) > 0)
       | select((.CpuArchitecture // "") == "X86")
       | select(.EniTrunkSupported == true)
       | select(.InstanceTypeId as $id | $available | index($id))
       | {id: .InstanceTypeId, gpu: .GPUAmount, cpu: .CpuCoreCount, memory: .MemorySize}]
    | sort_by(.gpu, .cpu, .memory, .id)
    | .[0].id // empty
  ')"
if [[ -n "${TEST_GPU_INSTANCE_TYPES}" ]]; then
  TEST_GPU_ZONES="$(jq -r --arg id "${TEST_GPU_INSTANCE_TYPES}" '[.[] | select(.id == $id) | .zone] | unique | join(",")' "${STOCK_JSON}")"
  export TEST_GPU_INSTANCE_TYPES TEST_GPU_ZONES
else
  unset TEST_GPU_INSTANCE_TYPES TEST_GPU_ZONES
fi

TEST_ZONE_A="$(jq -r 'map(.zone) | unique | .[0] // empty' <<<"${VSWITCH_DATA}")"
TEST_ZONE_B="$(jq -r 'map(.zone) | unique | .[1] // empty' <<<"${VSWITCH_DATA}")"
TEST_VSWITCH_ZONE_A="$(jq -r --arg zone "${TEST_ZONE_A}" '[.[] | select(.zone == $zone) | .id][0] // empty' <<<"${VSWITCH_DATA}")"
TEST_VSWITCH_ZONE_B="$(jq -r --arg zone "${TEST_ZONE_B}" '[.[] | select(.zone == $zone) | .id][0] // empty' <<<"${VSWITCH_DATA}")"
export TEST_ZONE_A TEST_ZONE_B TEST_VSWITCH_ZONE_A TEST_VSWITCH_ZONE_B

if [[ "${CLUSTER_IP_STACK}" == *ipv6* ]]; then
  TEST_IP_FAMILY="ipv6"
  export TEST_IP_FAMILY
else
  unset TEST_IP_FAMILY
fi

unset TEST_CAPACITY_RESERVATION_ID TEST_CAPACITY_RESERVATION_INSTANCE_TYPE
export ALLOW_UNSAFE_E2E_CLUSTER=true
export GOFLAGS="${E2E_GOFLAGS:--mod=mod -tags=integration}"

DEFAULT_LABEL_FILTER='!launch-template && !capacity-reservation && !reservation && !reserved && !repair && !metadata-options && !block-device'
LABEL_FILTER="${DEFAULT_LABEL_FILTER}"
if [[ -n "${EXTRA_LABEL_FILTER:-}" ]]; then
  LABEL_FILTER="(${DEFAULT_LABEL_FILTER}) && (${EXTRA_LABEL_FILTER})"
fi

: >"${DISCOVERED_ENV}"
for variable in KUBECONFIG TEST_CLUSTER_NAME TEST_CLUSTER_ID TEST_CLUSTER_ENDPOINT TEST_REGION \
  TEST_RAM_ROLE TEST_VSWITCH_IDS TEST_SECURITY_GROUP_IDS TEST_SECURITY_GROUP_ID TEST_IMAGE_ID \
  TEST_ZONES TEST_ZONE_A TEST_ZONE_B TEST_VSWITCH_ZONE_A TEST_VSWITCH_ZONE_B TEST_INSTANCE_TYPES \
  TEST_GPU_INSTANCE_TYPES TEST_GPU_ZONES TEST_IP_FAMILY ALLOW_UNSAFE_E2E_CLUSTER GOFLAGS; do
  if [[ -n "${!variable+x}" && -n "${!variable}" ]]; then
    printf 'export %s=%q\n' "${variable}" "${!variable}" >>"${DISCOVERED_ENV}"
  fi
done

log "Cluster: ${TEST_CLUSTER_NAME} (${TEST_CLUSTER_ID})"
log "Region: ${TEST_REGION}; endpoint: ${TEST_CLUSTER_ENDPOINT}; IP stack: ${CLUSTER_IP_STACK}"
log "VSwitches: ${TEST_VSWITCH_IDS}; zones: ${TEST_ZONES}"
log "Security groups: ${TEST_SECURITY_GROUP_IDS}; image: ${TEST_IMAGE_ID}"
log "Instance types: ${TEST_INSTANCE_TYPES}; GPU: ${TEST_GPU_INSTANCE_TYPES:-not available}"
log "Excluded labels: ${LABEL_FILTER}"
log "Discovered environment: ${DISCOVERED_ENV} (cloud credentials intentionally omitted)"

if [[ "${DRY_RUN:-0}" == "1" ]]; then
  printf 'discovery\tPASS\t0\t0\n' >>"${SUMMARY}"
  log "Dry run complete: ${RESULT_DIR}"
  exit 0
fi

if [[ "${ALLOW_CONCURRENT_E2E:-false}" != "true" ]] && pgrep -f '[g]o test .*test/suites/' >/dev/null 2>&1; then
  fail "another integration test process is running; wait for it or set ALLOW_CONCURRENT_E2E=true"
fi

if [[ "${SKIP_KARPENTER_READY_CHECK:-0}" != "1" ]]; then
  kubectl --kubeconfig "${KUBECONFIG}" -n karpenter rollout status deployment/karpenter --timeout=180s 2>&1 | tee -a "${ALL_LOG}" || fail "Karpenter deployment is not ready"
fi

failures=0
for suite_dir in "${REPO_ROOT}"/test/suites/*; do
  [[ -d "${suite_dir}" ]] || continue
  suite="$(basename "${suite_dir}")"
  if [[ "${suite}" == "ipv6" && "${CLUSTER_IP_STACK}" != *ipv6* ]]; then
    printf '%s\tSKIP_IPV4_CLUSTER\t0\t0\n' "${suite}" | tee -a "${SUMMARY}" "${ALL_LOG}"
    continue
  fi

  started_at="$(date +%s)"
  log "===== START ${suite} $(date -u +%Y-%m-%dT%H:%M:%SZ) ====="
  suite_log="${RESULT_DIR}/${suite}.log"
  : >"${suite_log}"

  if ! refresh_kubeconfig; then
    duration=$(( $(date +%s) - started_at ))
    printf '%s\tFAIL_KUBECONFIG\t1\t%s\n' "${suite}" "${duration}" | tee -a "${SUMMARY}" "${ALL_LOG}"
    failures=$((failures + 1))
    continue
  fi

  args=(
    go test -p 1 -count 1 -timeout 12h -v
    "./test/suites/${suite}/..."
    --ginkgo.timeout=3h20m
    --ginkgo.grace-period=3m
    --ginkgo.vv
    "--ginkgo.label-filter=${LABEL_FILTER}"
  )

  set +e
  (
    cd "${REPO_ROOT}"
    "${args[@]}"
  ) 2>&1 | tee -a "${ALL_LOG}" "${suite_log}"
  status=${PIPESTATUS[0]}
  set -e

  duration=$(( $(date +%s) - started_at ))
  if [[ "${status}" -eq 0 ]]; then
    result="PASS"
  else
    result="FAIL"
    failures=$((failures + 1))
  fi
  printf '%s\t%s\t%s\t%s\n' "${suite}" "${result}" "${status}" "${duration}" | tee -a "${SUMMARY}" "${ALL_LOG}"
  log "===== END ${suite} ${result} ====="
done

log "Complete: ${RESULT_DIR}; failed suites: ${failures}"
if [[ "${failures}" -gt 0 ]]; then
  exit 1
fi
