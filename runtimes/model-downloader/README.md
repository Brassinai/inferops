# InferOps Model Cache Downloader

A small, non-privileged container image that downloads model artifacts into a
staging directory and atomically publishes them by writing a completion marker.

## Behavior

- Downloads into `--staging-subpath` under `--cache-root`.
- Writes `.inferops-cache.json` in staging, then atomically publishes the
  completed directory at `--dest-subpath`.
- If the destination already contains a marker for the same `input-hash` and
  revision, exits successfully without downloading again.
- Refuses to overwrite a destination with a different identity.
- Cleans only the current attempt's staging directory on failure.

## Security

- Runs as non-root (UID 65532).
- Read-only root filesystem.
- Drops all capabilities.
- No privilege escalation.
- No Kubernetes service account token automount.
- Reads `HF_TOKEN` only from the referenced Secret.

## Supported sources

Month one supports Hugging Face. S3-compatible sources are part of MVP-503 and
are not accepted by the current controller API.
