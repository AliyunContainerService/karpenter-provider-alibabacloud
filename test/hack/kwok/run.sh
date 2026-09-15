#!/usr/bin/env bash
set -euo pipefail

: "${KARPENTER_CORE_DIR:?KARPENTER_CORE_DIR is required}"
: "${KWOK_CLUSTER_NAME:=karpenter-kwok-e2e}"
: "${KWOK_NODECLASS:?KWOK_NODECLASS is required}"
: "${KWOK_NODEPOOL:?KWOK_NODEPOOL is required}"
: "${UPSTREAM_TEST_SUITE:=regression}"
: "${FOCUS:=}"
: "${SKIP:=}"
: "${E2E_GOFLAGS:=-mod=mod}"
: "${KWOK_CLEANUP:=true}"
: "${KWOK_LARGE_SCALE:=true}"
: "${KWOK_SCALE_REPLICAS:=1000}"
: "${KWOK_SCALE_TIMEOUT:=30m}"
: "${KWOK_REPORT_DIR:=}"

suite="$(echo "${UPSTREAM_TEST_SUITE}" | tr A-Z a-z)"
focus="${FOCUS}"
if [[ "${suite}" == "performance" && ! -d "${KARPENTER_CORE_DIR}/test/suites/performance" ]]; then
  echo "upstream performance suite is not present in ${KARPENTER_CORE_DIR}; pin a compatible suite or replacement contract" >&2
  exit 1
fi

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "$1 is required for KWOK e2e" >&2
    exit 1
  fi
}

cleanup() {
  if [[ "${KWOK_CLEANUP}" != "true" ]]; then
    return
  fi
  kubectl delete namespace kwok-scale --ignore-not-found=true --wait=false >/dev/null 2>&1 || true
  kubectl delete nodepools --all --ignore-not-found=true >/dev/null 2>&1 || true
  kubectl delete kwoknodeclasses --all --ignore-not-found=true >/dev/null 2>&1 || true
  (cd "${KARPENTER_CORE_DIR}" && helm uninstall karpenter --namespace kube-system >/dev/null 2>&1) || true
  (cd "${KARPENTER_CORE_DIR}" && UNINSTALL=true ./hack/install-kwok.sh >/dev/null 2>&1) || true
  kind delete cluster --name "${KWOK_CLUSTER_NAME}" >/dev/null 2>&1 || true
}

write_report() {
  if [[ -z "${KWOK_REPORT_DIR}" ]]; then
    return
  fi
  mkdir -p "${KWOK_REPORT_DIR}"
  kubectl get nodes -o wide >"${KWOK_REPORT_DIR}/nodes.txt" 2>&1 || true
  kubectl get nodeclaims -A -o wide >"${KWOK_REPORT_DIR}/nodeclaims.txt" 2>&1 || true
  kubectl get pods -A -o wide >"${KWOK_REPORT_DIR}/pods.txt" 2>&1 || true
  kubectl get events -A --sort-by=.lastTimestamp >"${KWOK_REPORT_DIR}/events.txt" 2>&1 || true
  kubectl logs -n kube-system deployment/karpenter --all-containers --tail=-1 >"${KWOK_REPORT_DIR}/karpenter.log" 2>&1 || true
}

run_large_scale_check() {
  if [[ "${KWOK_LARGE_SCALE}" != "true" ]]; then
    return
  fi

  kubectl create namespace kwok-scale --dry-run=client -o yaml | kubectl apply -f -
  kubectl apply -f "${KWOK_NODECLASS}"
  kubectl apply -f "${KWOK_NODEPOOL}"
  kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kwok-scale
  namespace: kwok-scale
spec:
  replicas: ${KWOK_SCALE_REPLICAS}
  selector:
    matchLabels:
      app: kwok-scale
  template:
    metadata:
      labels:
        app: kwok-scale
    spec:
      terminationGracePeriodSeconds: 0
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.9
          resources:
            requests:
              cpu: 100m
              memory: 64Mi
EOF

  kubectl rollout status deployment/kwok-scale -n kwok-scale --timeout="${KWOK_SCALE_TIMEOUT}"
  ready="$(kubectl get deployment kwok-scale -n kwok-scale -o jsonpath='{.status.readyReplicas}')"
  nodeclaims="$(kubectl get nodeclaims -o name | wc -l | tr -d '[:space:]')"
  nodes="$(kubectl get nodes --no-headers | wc -l | tr -d '[:space:]')"

  if [[ "${ready:-0}" != "${KWOK_SCALE_REPLICAS}" ]]; then
    echo "expected ${KWOK_SCALE_REPLICAS} ready KWOK scale pods, got ${ready:-0}" >&2
    write_report
    exit 1
  fi
  if [[ "${nodeclaims}" == "0" || "${nodes}" == "0" ]]; then
    echo "expected KWOK scale check to create nodes and nodeclaims; nodes=${nodes}, nodeclaims=${nodeclaims}" >&2
    write_report
    exit 1
  fi

  if [[ -n "${KWOK_REPORT_DIR}" ]]; then
    cat >"${KWOK_REPORT_DIR}/summary.txt" <<EOF
kwok_large_scale=true
replicas=${KWOK_SCALE_REPLICAS}
ready=${ready}
nodes=${nodes}
nodeclaims=${nodeclaims}
EOF
    write_report
  fi
}

require kind
require kubectl
require helm
require ko

if ! kind get clusters | grep -qx "${KWOK_CLUSTER_NAME}"; then
  kind create cluster --name "${KWOK_CLUSTER_NAME}"
fi

trap cleanup EXIT

kubectl config use-context "kind-${KWOK_CLUSTER_NAME}"
kubectl taint nodes "${KWOK_CLUSTER_NAME}-control-plane" CriticalAddonsOnly:NoSchedule --overwrite

cd "${KARPENTER_CORE_DIR}"
./hack/install-kwok.sh

export KWOK_REPO=kind.local
export KIND_CLUSTER_NAME="${KWOK_CLUSTER_NAME}"
controller_img="$(GOFLAGS="${E2E_GOFLAGS}" KO_DOCKER_REPO="${KWOK_REPO}" ko build sigs.k8s.io/karpenter/kwok)"
img_repository="$(echo "${controller_img}" | cut -d ":" -f 1)"
img_tag="$(echo "${controller_img}" | cut -d ":" -f 2 -s)"

kubectl apply -f kwok/charts/crds
helm upgrade --install karpenter kwok/charts --namespace kube-system --skip-crds \
  --set logLevel=debug \
  --set controller.resources.requests.cpu=1 \
  --set controller.resources.requests.memory=1Gi \
  --set controller.resources.limits.cpu=1 \
  --set controller.resources.limits.memory=1Gi \
  --set settings.featureGates.nodeRepair=true \
  --set settings.featureGates.staticCapacity=true \
  --set controller.image.repository="${img_repository}" \
  --set controller.image.tag="${img_tag}" \
  --set serviceMonitor.enabled=false

kubectl rollout status deployment/karpenter -n kube-system --timeout=5m

cd test
GOFLAGS="${E2E_GOFLAGS}" go test \
  -count 1 \
  -timeout 12h \
  -v \
  "./suites/${suite}/..." \
  --ginkgo.focus="${focus}" \
  --ginkgo.skip="${SKIP}" \
  --ginkgo.timeout=3h \
  --ginkgo.grace-period=5m \
  --ginkgo.vv \
  --default-nodeclass="${KWOK_NODECLASS}" \
  --default-nodepool="${KWOK_NODEPOOL}"

run_large_scale_check
