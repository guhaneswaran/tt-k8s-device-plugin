# Small image with Tenstorrent userspace tools baked in, for in-cluster
# hardware verification (tt-smi telemetry, board listing, reset).
#
# tt-smi ships as a pip wheel (pyluwen underneath) — there is no official
# standalone tt-smi image, so we bake our own. Pinned to match the host.
#
# Build (no COPY, so no build context needed):
#   docker build -t tt-tools:3.0.38 - < hack/tt-tools.Dockerfile
FROM python:3.10-slim

ARG TT_SMI_VERSION=3.0.38
LABEL org.opencontainers.image.title="tt-tools" \
      org.opencontainers.image.description="Tenstorrent userspace tools (tt-smi) for in-cluster hardware verification" \
      org.opencontainers.image.vendor="Tenstorrent Inc."
RUN pip install --no-cache-dir "tt-smi==${TT_SMI_VERSION}"

# Default to listing boards; override args in the pod for snapshots/reset.
ENTRYPOINT ["tt-smi"]
CMD ["-ls"]
