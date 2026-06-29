# hack/ — dev & verification tooling

Local workflow for iterating on the Tenstorrent device plugin against a real
card in a minikube cluster. Designed to mirror CI/CD (deploys the same Helm
chart `release.yml` publishes).

## Scripts

| Script | What it does |
|--------|--------------|
| [`dev-deploy.sh`](dev-deploy.sh) | One-command loop: **check → build → load → prune → `helm upgrade --install` → verify discovery → hardware verify**. Orchestrates the other two. |
| [`check.sh`](check.sh) | Runs `ci.yml` locally: `go build`, `go vet`, `go test -race`, `golangci-lint`, `helm lint`/`template`. Reports all failures; non-zero exit gates the deploy. |
| [`tt-verify-hw.sh`](tt-verify-hw.sh) | Builds the `tt-tools` image, runs [`tt-hwcheck.py`](tt-hwcheck.py) in a pod on the card, leaves it running as an inspectable test pod. |
| [`tt-hwcheck.py`](tt-hwcheck.py) | In-pod checks via the plugin-mounted `/sys` + `/dev/tenstorrent`: identity/firmware, temp vs `temp1_max`, PCIe link width/speed, heartbeat, `tt-smi` enumeration. |
| [`tt-tools.Dockerfile`](tt-tools.Dockerfile) | Small `python-slim` + `tt-smi` image for verification. |
| [`lib.sh`](lib.sh) | Shared presentation + helpers sourced by the scripts. Not run directly. |

## Usage

```bash
./hack/dev-deploy.sh              # full loop on the n150
./hack/dev-deploy.sh --no-check   # skip CI gate (faster)
./hack/dev-deploy.sh --no-probe   # deploy only, no hardware verify
SKIP_BUILD=1 ./hack/dev-deploy.sh # reuse last image
./hack/check.sh                   # just the CI checks
./hack/tt-verify-hw.sh            # just the hardware verify
```

Pass `-h`/`--help` to any script for its full flags and env knobs.

## Notes

- Single dev environment (minikube, docker driver, device passed through).
- `KEEP_IMAGES` (default 2) bounds both `dev-*` images (minikube + host) and Helm
  revisions, with dangling-image/build-cache pruning each build.
- The hardware-verify pod holds the single card while alive — `kubectl delete pod
  tt-hw-verify` (or the next run) frees it for a real workload.
- All scripts honor `NO_COLOR` and auto-disable color when output isn't a TTY.
