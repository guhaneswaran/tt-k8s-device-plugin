#!/usr/bin/env bash
#
# lib.sh - shared presentation + helpers for the hack/ scripts.
#          Source it; do not execute it directly.
#
# Provides: colored output helpers (banner/step/ok/info/warn/bad/kv/fail/rule),
# dimmed command output (dimout/run_dim), and small k8s/docker utilities
# (need/wait_for_allocatable/mk_has_image/prune_dev_images).
#
# Conventions: scripts set STEP=0 and TOTAL=<n> before calling step().

# Guard against double-sourcing.
[[ -n "${_TT_LIB_SOURCED:-}" ]] && return 0
_TT_LIB_SOURCED=1

# ---- colors (auto-disable on non-tty or when NO_COLOR is set) ----------------
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; RESET=$'\033[0m'
  RED=$'\033[31m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; BLUE=$'\033[34m'; CYAN=$'\033[36m'
  # 256-color accents
  VIOLET=$'\033[38;5;141m'; BROWN=$'\033[38;5;137m'
else
  BOLD=""; DIM=""; RESET=""; RED=""; GREEN=""; YELLOW=""; BLUE=""; CYAN=""
  VIOLET=""; BROWN=""
fi

_TT_WIDTH=58
_TT_RULE="$(printf '─%.0s' $(seq 1 "${_TT_WIDTH}"))"
STEP="${STEP:-0}"
TOTAL="${TOTAL:-0}"

# ---- presentation -----------------------------------------------------------
rule()  { printf '%s%s%s\n' "${VIOLET}" "${_TT_RULE}" "${RESET}"; }

# banner "Title" — draws a violet box sized to _TT_WIDTH, title left-aligned.
banner() {
  local title="$1" pad
  pad=$(( _TT_WIDTH - ${#title} - 2 )); (( pad < 0 )) && pad=0
  printf '\n%s┌%s┐%s\n'    "${VIOLET}" "${_TT_RULE}" "${RESET}"
  printf '%s│%s  %s%s%s%*s%s│%s\n' "${VIOLET}" "${RESET}" "${BOLD}${VIOLET}" "${title}" "${RESET}" "${pad}" "" "${VIOLET}" "${RESET}"
  printf '%s└%s┘%s\n'      "${VIOLET}" "${_TT_RULE}" "${RESET}"
}

step()  { STEP=$((STEP + 1)); printf '\n%s[%d/%d]%s %s%s%s\n' "${VIOLET}${BOLD}" "${STEP}" "${TOTAL}" "${RESET}" "${BOLD}" "$*" "${RESET}"; }
ok()    { printf '  %s✓%s %s\n' "${GREEN}"  "${RESET}" "$*"; }
info()  { printf '  %s•%s %s\n' "${CYAN}"   "${RESET}" "$*"; }
warn()  { printf '  %s⚠%s %s\n' "${YELLOW}" "${RESET}" "$*"; }
bad()   { printf '  %s✗%s %s\n' "${RED}"    "${RESET}" "$*"; }
kv()    { printf '  %s%-22s%s %s\n' "${BROWN}" "$1" "${RESET}" "$2"; }
fail()  { printf '\n  %s✗ ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$*" >&2; exit 1; }

# Indent + dim a command's stdout/stderr.
dimout() { sed "s/^/    ${DIM}/; s/\$/${RESET}/"; }

# run_dim CMD...  — run a command, dim its output, return the command's exit code
# (not sed's). Use in `if run_dim ...; then`.
run_dim() { "$@" 2>&1 | dimout; return "${PIPESTATUS[0]}"; }

# ---- prerequisites ----------------------------------------------------------
# need CMD [hint] — fail with a clear message if CMD is missing.
need() { command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1${2:+ ($2)}"; }

# ---- kubernetes / minikube / docker utilities -------------------------------
# mk_has_image TAG — true if minikube already has an image matching TAG.
mk_has_image() { minikube image ls 2>/dev/null | grep -qF "$1"; }

# wait_for_allocatable NODE RESOURCE [TRIES] — poll node allocatable until the
# resource count is > 0 (a plugin redeploy briefly reports 0). Echoes the count
# and returns 0 on success; returns 1 on timeout (TRIES*2s, default 30 => 60s).
wait_for_allocatable() {
  local node="$1" res="$2" tries="${3:-30}" alloc="" i
  for (( i = 0; i < tries; i++ )); do
    alloc="$(kubectl get node "${node}" -o jsonpath="{.status.allocatable['${res//./\\.}']}" 2>/dev/null || true)"
    [[ -n "${alloc}" && "${alloc}" != "0" ]] && { printf '%s' "${alloc}"; return 0; }
    sleep 2
  done
  return 1
}

# prune_dev_images IMAGE_NAME KEEP — keep the newest KEEP IMAGE_NAME:dev-* images
# in minikube and host docker, remove the rest, then sweep dangling images and
# host build cache (the real disk hogs over many builds).
prune_dev_images() {
  local img_name="$1" keep="$2" prefix="$1:dev-" img freed
  [[ "${keep}" =~ ^[0-9]+$ ]] || return 0
  while IFS= read -r img; do
    [[ -n "${img}" ]] && minikube image rm "${img}" >/dev/null 2>&1 && info "purged ${img} (minikube)"
  done < <(minikube image ls 2>/dev/null | grep -F "${prefix}" | sort -r | tail -n +"$((keep + 1))")
  while IFS= read -r img; do
    [[ -n "${img}" ]] && docker rmi "${img}" >/dev/null 2>&1 && info "purged ${img} (host docker)"
  done < <(docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep -F "${prefix}" | sort -r | tail -n +"$((keep + 1))")
  freed="$(docker image prune -f 2>/dev/null | grep -i reclaimed || true)"
  [[ -n "${freed}" ]] && info "dangling images: ${freed}"
  freed="$(docker builder prune -f 2>/dev/null | grep -i reclaimed || true)"
  [[ -n "${freed}" ]] && info "build cache: ${freed}"
  return 0
}
