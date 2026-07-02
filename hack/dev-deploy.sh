#!/usr/bin/env bash
#
# dev-deploy.sh - one-command dev loop for the TT device plugin:
#   check (ci.yml) -> build -> load -> prune -> helm upgrade --install
#   -> verify discovery -> hardware verify (tt-smi on the card).
#
# CDI mode is ON by default (needs a CDI-aware runtime: containerd 1.7+/2.x or
# CRI-O). Use --no-cdi for the legacy device-node/mount path.
#
# Flags:
#   --no-cdi      deploy the legacy path (cdi.enabled=false)
#   --no-check    skip the local CI gate (check.sh)
#   --no-probe    skip the hardware verify (tt-verify-hw.sh)
#   -h, --help    show this help
#
# Env:
#   CDI=0         same as --no-cdi
#   SKIP_CHECK=1  same as --no-check       SKIP_BUILD=1  reuse last loaded image
#   KEEP_IMAGES=N keep N dev images + helm revisions (default 2)
#   IMAGE_NAME / NAMESPACE / RESOURCE / RELEASE / NODE  override defaults
#   NO_COLOR=1    disable colored output
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_DIR}"
# shellcheck source=hack/lib.sh
source "${SCRIPT_DIR}/lib.sh"

# Print the leading comment block (after the shebang) as help text.
usage() { awk 'NR==1{next} /^#/{sub(/^# ?/,"");print;next} {exit}' "${BASH_SOURCE[0]}"; }

# ---- config -----------------------------------------------------------------
IMAGE_NAME="${IMAGE_NAME:-tt-device-plugin}"
NAMESPACE="${NAMESPACE:-kube-system}"
RESOURCE="${RESOURCE:-tenstorrent.com/n150}"
RELEASE="${RELEASE:-tt-device-plugin}"
NODE="${NODE:-minikube}"
DS_NAME="tt-device-plugin"
CHART_DIR="helm/tt-device-plugin"
KEEP_IMAGES="${KEEP_IMAGES:-2}"

IMAGE_TAG="dev-$(date +%Y%m%d-%H%M%S)"     # unique tag so helm always rolls
TAG="${IMAGE_NAME}:${IMAGE_TAG}"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"

DO_PROBE=1
DO_CHECK=1
DO_CDI=1
[[ "${SKIP_CHECK:-0}" == "1" ]] && DO_CHECK=0
[[ "${CDI:-1}" == "0" ]] && DO_CDI=0
while (( $# )); do
  case "$1" in
    --no-cdi)   DO_CDI=0 ;;
    --no-probe) DO_PROBE=0 ;;
    --no-check) DO_CHECK=0 ;;
    -h|--help)  usage; exit 0 ;;
    *) printf 'unknown argument: %s\n\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

# CDI spec file the plugin writes on the node, derived from the resource class.
RESOURCE_CLASS="${RESOURCE##*/}"
CDI_SPEC="/var/run/cdi/tenstorrent-${RESOURCE_CLASS}.json"

trap 'rc=$?; (( rc )) && printf "\n  %s✗ aborted%s (exit %d)\n" "${RED}${BOLD}" "${RESET}" "${rc}" >&2' ERR

TOTAL=5
(( DO_CHECK )) && TOTAL=$((TOTAL + 1))
(( DO_PROBE )) && TOTAL=$((TOTAL + 1))
START_TS=$(date +%s)

banner "Tenstorrent device-plugin - dev deploy"
kv "image"        "${TAG}"
kv "version"      "${VERSION}"
kv "helm release" "${RELEASE}"
kv "resource"     "${RESOURCE}"
kv "namespace"    "${NAMESPACE}"
kv "mode"         "$( (( DO_CDI )) && echo CDI || echo legacy )"

# ---- prerequisites ----------------------------------------------------------
step "Checking prerequisites"
need docker; need minikube; need kubectl; need helm
ok "docker, minikube, kubectl, helm present"
minikube status >/dev/null 2>&1 || fail "minikube is not running (try: minikube start)"
ok "minikube is running"
# The plugin fatal-exits with "No Tenstorrent devices found" if the card isn't
# visible inside the node, so check that first.
docker exec "${NODE}" test -e /dev/tenstorrent/0 2>/dev/null \
  || fail "/dev/tenstorrent/0 not present inside the ${NODE} node — device passthrough missing"
ok "n150 visible inside ${NODE} node"
# CDI needs a CDI-aware runtime; docker/cri-dockerd silently ignores CDI devices,
# so the container would come up with no device. Catch that before deploying.
if (( DO_CDI )); then
  RUNTIME="$(kubectl get node "${NODE}" -o jsonpath='{.status.nodeInfo.containerRuntimeVersion}' 2>/dev/null || true)"
  case "${RUNTIME}" in
    containerd://*|cri-o://*) ok "CDI-aware runtime: ${RUNTIME}" ;;
    *) fail "CDI requested but node runtime is '${RUNTIME:-unknown}' — needs containerd 1.7+/2.x or CRI-O (recreate: minikube delete && minikube start --container-runtime=containerd)" ;;
  esac
fi

# ---- pre-flight: local CI checks --------------------------------------------
if (( DO_CHECK )); then
  step "Running local CI checks (check.sh)"
  "${SCRIPT_DIR}/check.sh" && ok "local checks passed" \
    || fail "local checks failed — fix them, or rerun with --no-check / SKIP_CHECK=1"
fi

# ---- build + load -----------------------------------------------------------
step "Building image"
if [[ "${SKIP_BUILD:-0}" == "1" ]]; then
  warn "SKIP_BUILD=1 — reusing latest loaded image"
  TAG="$(minikube image ls 2>/dev/null | grep -F "${IMAGE_NAME}:dev-" | sort | tail -1)"
  [[ -n "${TAG}" ]] || fail "no previously loaded ${IMAGE_NAME}:dev-* image found"
  IMAGE_TAG="${TAG##*:}"
  info "reusing ${BOLD}${TAG}${RESET}"
  step "Loading image into minikube"; info "skipped (SKIP_BUILD)"
else
  info "tag ${BOLD}${TAG}${RESET}"
  run_dim docker build --build-arg "VERSION=${VERSION}" -t "${TAG}" . || fail "docker build failed"
  ok "image built"
  step "Loading image into minikube"
  minikube image load "${TAG}"
  ok "loaded into node docker"
  info "pruning old dev images (keeping newest ${KEEP_IMAGES})…"
  prune_dev_images "${IMAGE_NAME}" "${KEEP_IMAGES}"
fi

# ---- deploy -----------------------------------------------------------------
# Deploy via the Helm chart (the artifact CI/CD publishes), overriding only the
# image; IfNotPresent so k8s never tries to pull our local-only tag.
step "Deploying via Helm chart"
# A DaemonSet left by an earlier raw `kubectl apply` isn't Helm-managed and Helm
# refuses to adopt it — remove it once so Helm can take ownership.
if kubectl get ds -n "${NAMESPACE}" "${DS_NAME}" >/dev/null 2>&1; then
  managed="$(kubectl get ds -n "${NAMESPACE}" "${DS_NAME}" \
    -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}' 2>/dev/null || true)"
  if [[ "${managed}" != "Helm" ]]; then
    warn "found non-Helm DaemonSet — removing so Helm can own it"
    kubectl delete ds -n "${NAMESPACE}" "${DS_NAME}" --wait=true | dimout
  fi
fi
info "helm upgrade --install ${BOLD}${RELEASE}${RESET}"
run_dim helm upgrade --install "${RELEASE}" "${CHART_DIR}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set image.repository="${IMAGE_NAME}" \
  --set image.tag="${IMAGE_TAG}" \
  --set image.pullPolicy=IfNotPresent \
  --set cdi.enabled="$( (( DO_CDI )) && echo true || echo false )" \
  --history-max "${KEEP_IMAGES}" \
  --wait --timeout 120s || fail "helm upgrade failed"
ok "Helm release deployed"

# ---- verify discovery -------------------------------------------------------
step "Verifying discovery"
printf '  %sPlugin logs:%s\n' "${DIM}" "${RESET}"
kubectl -n "${NAMESPACE}" logs "ds/${DS_NAME}" --tail=10 | dimout
# The plugin re-registers after the rollout; the node reports 0 in between.
if ALLOC="$(wait_for_allocatable "${NODE}" "${RESOURCE}" 30)"; then
  ok "node advertises ${BOLD}${RESOURCE} = ${ALLOC}${RESET}"
else
  fail "node does not advertise ${RESOURCE} after 60s — check plugin logs above"
fi

# In CDI mode the plugin must have written its spec where the runtime reads it;
# without it, Allocate hands out names the runtime cannot resolve.
if (( DO_CDI )); then
  if docker exec "${NODE}" test -f "${CDI_SPEC}" 2>/dev/null; then
    ok "CDI spec present on node: ${BOLD}${CDI_SPEC}${RESET}"
  else
    fail "CDI spec ${CDI_SPEC} not found on node — check plugin logs above"
  fi
fi

# ---- hardware verify --------------------------------------------------------
# Delegate to tt-verify-hw.sh: tt-smi on the card via the tt-tools image, proving
# the allocation reaches real silicon. It auto-skips its build if tt-tools is
# already loaded. Non-fatal here — report but don't abort the deploy.
if (( DO_PROBE )); then
  step "Hardware verify (tt-verify-hw.sh)"
  "${SCRIPT_DIR}/tt-verify-hw.sh" && ok "hardware verification passed" \
    || warn "hardware verification reported issues (see output above)"
fi

# ---- summary ----------------------------------------------------------------
ELAPSED=$(( $(date +%s) - START_TS ))
printf '\n'; rule
printf ' %s✓ Done%s  %sdeployed%s %s  %s(%ds)%s\n' \
  "${GREEN}${BOLD}" "${RESET}" "${DIM}" "${RESET}" "${BOLD}${TAG}${RESET}" "${DIM}" "${ELAPSED}" "${RESET}"
rule
