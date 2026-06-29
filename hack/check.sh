#!/usr/bin/env bash
#
# check.sh - run the same checks as .github/workflows/ci.yml, locally:
#            go build, go vet, go test -race, golangci-lint, helm lint+template.
#            (CodeQL is GitHub-only and skipped.)
#
# Runs every check and reports ALL failures (does not stop at the first), then
# exits non-zero if any failed. Use it as a pre-push gate before dev-deploy.sh.
#
# NOT `set -e`: we deliberately run every check and tally the results.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_DIR}"
# shellcheck source=hack/lib.sh
source "${SCRIPT_DIR}/lib.sh"

usage() {
  cat <<EOF
Usage: ${0##*/} [-h]

Run the CI checks locally (build, vet, test -race, golangci-lint, helm).
Honors NO_COLOR. Exit code is non-zero if any check fails.
EOF
}
[[ "${1:-}" == "-h" || "${1:-}" == "--help" ]] && { usage; exit 0; }
[[ $# -gt 0 ]] && { printf 'unknown argument: %s\n\n' "$1" >&2; usage >&2; exit 2; }

CHART_DIR="helm/tt-device-plugin"
TOTAL=5
START_TS=$(date +%s)
FAILED=()
SKIPPED=()

# check NAME CMD... — run a check, dim output, tally pass/fail.
check() {
  local name="$1"; shift
  if run_dim "$@"; then ok "${name} passed"; else bad "${name} FAILED"; FAILED+=("${name}"); fi
}

banner "Tenstorrent device-plugin - local CI checks"

step "go build";       check build go build ./cmd/tt-device-plugin/
step "go vet";         check vet   go vet ./...
step "go test -race";  check test  go test -race ./...

step "golangci-lint"
# Prefer PATH, else fall back to GOPATH/bin (where `go install` drops it).
golangci="$(command -v golangci-lint || true)"
[[ -z "${golangci}" && -x "$(go env GOPATH)/bin/golangci-lint" ]] && golangci="$(go env GOPATH)/bin/golangci-lint"
if [[ -n "${golangci}" ]]; then
  check lint "${golangci}" run --timeout 5m
else
  warn "golangci-lint not installed — skipping (CI still runs it)"
  info "install: ${BOLD}go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest${RESET}"
  SKIPPED+=("lint")
fi

step "helm lint + template"
if command -v helm >/dev/null 2>&1; then
  check helm-lint helm lint "${CHART_DIR}"
  # Render to a throwaway dir (mirrors CI's `helm template >/dev/null`): we only
  # care that it renders, not the output.
  TMPL_DIR="$(mktemp -d)"; trap 'rm -rf "${TMPL_DIR}"' EXIT
  check helm-template helm template test "${CHART_DIR}" --output-dir "${TMPL_DIR}"
else
  warn "helm not installed — skipping"
  SKIPPED+=("helm")
fi

# ---- summary ----------------------------------------------------------------
ELAPSED=$(( $(date +%s) - START_TS ))
printf '\n'; rule
(( ${#SKIPPED[@]} )) && printf ' %s⚠ skipped:%s %s\n' "${YELLOW}" "${RESET}" "${SKIPPED[*]}"
if (( ${#FAILED[@]} == 0 )); then
  printf ' %s✓ All checks passed%s  %s(%ds)%s\n' "${GREEN}${BOLD}" "${RESET}" "${DIM}" "${ELAPSED}" "${RESET}"
  rule; exit 0
else
  printf ' %s✗ %d check(s) failed:%s %s  %s(%ds)%s\n' \
    "${RED}${BOLD}" "${#FAILED[@]}" "${RESET}" "${FAILED[*]}" "${DIM}" "${ELAPSED}" "${RESET}"
  rule; exit 1
fi
