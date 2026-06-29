# Tenstorrent Kubernetes Device Plugin

A [Kubernetes device plugin][dp] that makes Tenstorrent AI accelerators
schedulable in a cluster. Once the [tt-kmd][tt-kmd] driver is loaded on a node
and this plugin is running there, pods can request Tenstorrent cards as a normal
resource and the kubelet hands the device to the container.

## Supported devices

The plugin groups cards into resource classes by their reported card type:

| Resource | Cards |
|----------|-------|
| `tenstorrent.com/n150` | n150 |
| `tenstorrent.com/n300` | n300, n300l, n300s |
| `tenstorrent.com/blackhole` | p100a, p150a/b/c, p300a/b/c |
| `tenstorrent.com/grayskull` | e75, e150 |

## How it works

On each node the plugin scans `/dev/tenstorrent/*`, reads each card's type from
`/sys/class/tenstorrent`, registers one resource class per card type with the
kubelet, and reports device health (temperature and ARC heartbeat) on an
interval. When a pod is granted a card, the plugin injects the device node, a
`/sys` mount, 1G hugepages (if present), and a `TT_VISIBLE_DEVICES` env var.

## Prerequisites

- Kubernetes ≥ 1.18
- [tt-kmd][tt-kmd] loaded on every node with a card
- `/dev/tenstorrent/N` device nodes present on the host
- 1G hugepages (`/dev/hugepages-1G`) recommended for tt-metal workloads

## Deploy

Helm (recommended):

```bash
helm install tt-device-plugin helm/tt-device-plugin/ -n kube-system
```

Or the raw manifest:

```bash
kubectl apply -f deploy/daemonset.yaml
```

The plugin runs as a DaemonSet, so it lands on every node automatically.

## Requesting a device

Add the resource to a pod's limits:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: tt-workload
spec:
  containers:
    - name: app
      image: ghcr.io/tenstorrent/tt-metal/tt-metalium/ubuntu-22.04-dev-amd64:latest
      resources:
        limits:
          tenstorrent.com/n150: 1
```

Each allocated container receives:

- the `/dev/tenstorrent/N` device node (read-write)
- `/dev/hugepages-1G` (read-write, if present on the host)
- `/sys` (read-only)
- `TT_VISIBLE_DEVICES` listing the allocated device IDs

## Building

```bash
go build -o tt-device-plugin ./cmd/tt-device-plugin/   # binary
docker build -t tt-device-plugin .                     # container image
```

## Development

The [`hack/`](hack/) directory holds the local dev loop — build, load into
minikube, deploy via Helm, and verify against a real card with `tt-smi`:

```bash
./hack/dev-deploy.sh    # check -> build -> deploy -> verify on the card
./hack/check.sh         # run the CI checks locally
```

See [`hack/README.md`](hack/README.md) for the full workflow and flags.

## License

Apache License 2.0 — see [LICENSE](LICENSE).

[dp]: https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/
[tt-kmd]: https://github.com/tenstorrent/tt-kmd
