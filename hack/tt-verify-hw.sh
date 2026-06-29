#!/usr/bin/env bash
#
# tt-verify-hw.sh - build the tt-tools image, load it into minikube, and run a
#   pod that talks to the n150 via tt-hwcheck.py (telemetry vs limits, PCIe link,
#   firmware, heartbeat, tt-smi) to prove the allocation reaches real hardware.
#   The pod stays alive afterwards as an inspectable test pod.
#
# Flags:
#   --force-build  rebuild the tt-tools image even if already loaded
#   --no-build     never build (fail if the image isn't loaded)
#   -h, --help     show this help
#
# Env:
#   FORCE_BUILD=1 / SKIP_BUILD=1      same as the flags above
#   TT_SMI_VERSION=X                  tt-smi version to bake/run (default 3.0.38)
#   EXPECT_FW_BUNDLE / TEMP_WARN_FRAC / REQUIRE_FULL_PCIE_SPEED  tt-hwcheck knobs
#   RESOURCE / NODE / NO_COLOR        override defaults
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_DIR}"
# shellcheck source=hack/lib.sh
source "${SCRIPT_DIR}/lib.sh"

usage() { awk 'NR==1{next} /^#/{sub(/^# ?/,"");print;next} {exit}' "${BASH_SOURCE[0]}"; }

# ---- config -----------------------------------------------------------------
IMAGE="${IMAGE:-tt-tools}"
TT_SMI_VERSION="${TT_SMI_VERSION:-3.0.38}"
TAG="${IMAGE}:${TT_SMI_VERSION}"
RESOURCE="${RESOURCE:-tenstorrent.com/n150}"
NODE="${NODE:-minikube}"
POD="tt-hw-verify"
CM="tt-hwcheck"

while (( $# )); do
  case "$1" in
    --force-build) FORCE_BUILD=1 ;;
    --no-build)    SKIP_BUILD=1 ;;
    -h|--help)     usage; exit 0 ;;
    *) printf 'unknown argument: %s\n\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

trap 'rc=$?; (( rc )) && printf "\n  %s✗ aborted%s (exit %d)\n" "${RED}${BOLD}" "${RESET}" "${rc}" >&2' ERR

# Decide whether to build: skip automatically if the image is already loaded,
# unless FORCE_BUILD. SKIP_BUILD forces skip regardless.
AUTO_SKIPPED=0
if [[ "${FORCE_BUILD:-0}" != "1" && "${SKIP_BUILD:-0}" != "1" ]] && mk_has_image "${TAG}"; then
  SKIP_BUILD=1; AUTO_SKIPPED=1
fi
[[ "${FORCE_BUILD:-0}" == "1" ]] && SKIP_BUILD=0

TOTAL=4; [[ "${SKIP_BUILD:-0}" == "1" ]] && TOTAL=3
START_TS=$(date +%s)

banner "Tenstorrent - in-cluster hardware verify (tt-smi)"
kv "image"    "${TAG}"
kv "resource" "${RESOURCE}"

need minikube; need kubectl
[[ "${SKIP_BUILD:-0}" == "1" ]] || need docker

# ---- build + load -----------------------------------------------------------
if [[ "${SKIP_BUILD:-0}" == "1" ]]; then
  step "Image"
  if (( AUTO_SKIPPED )); then
    info "${BOLD}${TAG}${RESET} already loaded — skipping build (--force-build to rebuild)"
  else
    mk_has_image "${TAG}" || fail "--no-build/SKIP_BUILD set but ${TAG} is not loaded"
    info "reusing ${BOLD}${TAG}${RESET}"
  fi
else
  step "Building tt-tools image (${TAG})"
  run_dim docker build --build-arg "TT_SMI_VERSION=${TT_SMI_VERSION}" -t "${TAG}" - \
    < hack/tt-tools.Dockerfile || fail "docker build failed"
  ok "image built"
  step "Loading image into minikube"
  minikube image load "${TAG}"
  ok "loaded into node docker"
fi

# ---- free the card ----------------------------------------------------------
step "Preparing the ${RESOURCE##*/}"
# A leftover probe holds the only card, so a new pod would stay Pending. Release.
for p in tt-verify "${POD}"; do
  if kubectl get pod "${p}" >/dev/null 2>&1; then
    info "deleting pod ${p} to free the card"
    kubectl delete pod "${p}" --ignore-not-found --wait=true >/dev/null 2>&1 || true
  fi
done
if ALLOC="$(wait_for_allocatable "${NODE}" "${RESOURCE}" 30)"; then
  ok "${RESOURCE} allocatable = ${ALLOC}"
else
  fail "no allocatable ${RESOURCE} after 60s (is the plugin deployed/healthy?)"
fi

# ---- verify -----------------------------------------------------------------
step "Hardware checks on the card (tt-hwcheck)"
# Deliver tt-hwcheck.py via a ConfigMap so editing it takes effect with no image
# rebuild. The pod requests the card, so the plugin mounts /dev/tenstorrent + /sys.
kubectl create configmap "${CM}" \
  --from-file=tt-hwcheck.py="${REPO_DIR}/hack/tt-hwcheck.py" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
info "applied ${CM} ConfigMap"

# Pass-through tt-hwcheck tuning knobs if set in the environment.
ENV_LINES=""
for v in EXPECT_FW_BUNDLE TEMP_WARN_FRAC REQUIRE_FULL_PCIE_SPEED; do
  [[ -n "${!v:-}" ]] && ENV_LINES+="        - {name: ${v}, value: \"${!v}\"}"$'\n'
done

# The pod runs the check once, then sleeps so it doubles as an inspectable test
# pod. It holds the card until the next run (cleanup above) or a manual delete.
cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: ${POD}
spec:
  restartPolicy: Never
  containers:
    - name: tt-hwcheck
      image: ${TAG}
      imagePullPolicy: IfNotPresent
      command: ["sh", "-c"]
      args:
        - |
          python3 /opt/tt-hwcheck/tt-hwcheck.py; rc=\$?
          echo "### tt-hwcheck-exit=\$rc ###"
          echo "### pod staying alive — exec in: kubectl exec -it ${POD} -- sh (try: tt-smi -ls) ###"
          exec sleep infinity
$( [[ -n "${ENV_LINES}" ]] && printf '      env:\n%s' "${ENV_LINES}" )
      volumeMounts:
        - name: hwcheck
          mountPath: /opt/tt-hwcheck
      resources:
        limits:
          ${RESOURCE}: 1
  volumes:
    - name: hwcheck
      configMap:
        name: ${CM}
EOF

info "waiting for ${POD} to run the checks…"
RC=1
if kubectl wait --for=condition=Ready "pod/${POD}" --timeout=180s >/dev/null 2>&1; then
  # The check runs at startup before the pod sleeps; wait for the result marker.
  LOGS=""
  for (( i = 0; i < 60; i++ )); do
    LOGS="$(kubectl logs "${POD}" 2>/dev/null || true)"
    printf '%s' "${LOGS}" | grep -q "tt-hwcheck-exit=" && break
    sleep 2
  done
  printf '%s\n' "${LOGS}" | dimout
  if printf '%s' "${LOGS}" | grep -q "tt-hwcheck-exit=0"; then
    RC=0; ok "hardware checks passed"
  else
    RC=1; warn "hardware checks reported failures (see above)"
  fi
  info "pod left running — inspect: ${BOLD}kubectl exec -it ${POD} -- sh${RESET}"
  info "removed on the next run, or: kubectl delete pod ${POD}"
else
  warn "pod did not become ready; state + logs:"
  kubectl get pod "${POD}" -o wide | dimout
  kubectl logs "${POD}" 2>&1 | dimout || true
fi

# ---- summary ----------------------------------------------------------------
ELAPSED=$(( $(date +%s) - START_TS ))
printf '\n'; rule
if (( RC == 0 )); then
  printf ' %s✓ Done%s  %s(%ds)%s\n' "${GREEN}${BOLD}" "${RESET}" "${DIM}" "${ELAPSED}" "${RESET}"
else
  printf ' %s✗ Hardware checks failed%s  %s(%ds)%s\n' "${RED}${BOLD}" "${RESET}" "${DIM}" "${ELAPSED}" "${RESET}"
fi
rule
exit "${RC}"
