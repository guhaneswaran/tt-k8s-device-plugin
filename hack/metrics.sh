#!/usr/bin/env bash
#
# metrics.sh - view the TT device-plugin Prometheus metrics from your shell.
#   Finds the plugin pod, port-forwards its metrics port, and scrapes /metrics.
#
# Flags:
#   -a, --all       show all metrics (incl. Go/process runtime), not just tt_*
#   -w, --watch     refresh every 2s (Ctrl-C to stop)
#   -g, --grep P    only show lines matching extended-regex P
#   -h, --help      show this help
#
# Env:
#   NAMESPACE   plugin namespace (default: kube-system)
#   PORT        container metrics port (default: 9102)
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/lib.sh
source "${SCRIPT_DIR}/lib.sh"

NAMESPACE="${NAMESPACE:-kube-system}"
PORT="${PORT:-9102}"
SELECTOR="app.kubernetes.io/name=tt-device-plugin"
PATTERN='^tt_'
WATCH=0

usage() {
  cat <<EOF
Usage: ${0##*/} [-a] [-w] [-g PATTERN]

View the tt-device-plugin Prometheus metrics.
  -a, --all       show all metrics, not just tt_*
  -w, --watch     refresh every 2s
  -g, --grep P    filter to extended-regex P
  -h, --help      this help

Env: NAMESPACE (default kube-system), PORT (default 9102).
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -a|--all)   PATTERN='.'; shift ;;
    -w|--watch) WATCH=1; shift ;;
    -g|--grep)  PATTERN="${2:?-g needs a pattern}"; shift 2 ;;
    -h|--help)  usage; exit 0 ;;
    *) printf 'unknown argument: %s\n\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

command -v kubectl >/dev/null 2>&1 || fail "kubectl not found"
command -v curl >/dev/null 2>&1 || fail "curl not found"

POD="$(kubectl -n "${NAMESPACE}" get pod -l "${SELECTOR}" \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)"
[[ -n "${POD}" ]] || fail "no tt-device-plugin pod in namespace '${NAMESPACE}' (deployed?)"

# Let kubectl choose a free local port (":PORT") so we never collide with a
# leftover forward, then read the chosen port from its output.
PF_LOG="$(mktemp)"
kubectl -n "${NAMESPACE}" port-forward "${POD}" ":${PORT}" >"${PF_LOG}" 2>&1 &
PF=$!
trap 'kill "${PF}" 2>/dev/null; rm -f "${PF_LOG}"' EXIT

LOCAL=""
for _ in $(seq 1 40); do
  LOCAL="$(sed -nE 's/.*127\.0\.0\.1:([0-9]+) ->.*/\1/p' "${PF_LOG}" | head -1)"
  [[ -n "${LOCAL}" ]] && break
  kill -0 "${PF}" 2>/dev/null || fail "port-forward failed:$(printf '\n')$(cat "${PF_LOG}")"
  sleep 0.25
done
[[ -n "${LOCAL}" ]] || fail "port-forward did not become ready"

URL="localhost:${LOCAL}/metrics"

if [[ "${WATCH}" == "1" ]]; then
  command -v watch >/dev/null 2>&1 || fail "watch not installed"
  watch -n2 "curl -s '${URL}' | grep -E '${PATTERN}' | sort"
else
  banner "tt-device-plugin metrics — ${POD}  (:${PORT})"
  out="$(curl -s "${URL}" | grep -E "${PATTERN}" | sort)"
  [[ -n "${out}" ]] || fail "no metrics matched (endpoint reachable? pattern '${PATTERN}')"
  printf '%s\n' "${out}"
fi
