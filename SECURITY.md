# Security policy

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Use GitHub’s
private vulnerability reporting feature on the repository’s **Security** tab.
Include the affected version or commit, reproduction steps, impact, and any
suggested mitigation.

Maintainers will acknowledge a complete report as soon as practical, validate
the issue, coordinate a fix, and credit the reporter unless anonymity is
requested. Please allow time for a patch before public disclosure.

## Public repository hygiene

This repository and its full Git history are public. Contributors must use
synthetic fixtures and must not commit credentials, authorization headers,
private prompts or output, internal URLs, private configuration, customer data,
certificates, or key material. `.env`, local connection files, common key
formats, and local data files are excluded from Git and Docker contexts.

CI scans both reachable Git history and the checked-out tree for secret-shaped
content. This is a guardrail, not a guarantee. If sensitive material is ever
committed:

1. Revoke or rotate it immediately.
2. Report it through private vulnerability reporting.
3. Avoid copying the value into issues, pull requests, CI logs, or chat.
4. Coordinate any history rewrite after rotation; deleting the latest copy does
   not make an exposed secret safe again.

## Deployment assumptions

- `localm` must sit behind HTTPS when accessed over an untrusted network.
- The inference runtime should remain on a private network.
- `/docs/`, `/openapi.json`, `/healthz`, and `/readyz` are intentionally public.
- `/v1/chat/completions`, `/v1/responses`, `/v1/embeddings`, `/v1/models`, and
  `/v1/capabilities` require a configured bearer key.
- Client keys and `UPSTREAM_API_KEY` must be managed as deployment secrets.
- Named connections reference upstream secrets through `api_key_env`; they do
  not accept inline keys. Remote routing requires explicit file-level and
  per-connection trust settings.
- Rate limits are in memory and apply per replica, not globally.

LocalM logs method, route, status, and latency. It does not log or store client
IP addresses, request bodies, response bodies, authorization headers, upstream
URLs, upstream error bodies, or raw transport errors. Capability output is
authenticated and omits URLs and credentials. Public health endpoints expose
only service state. No telemetry or background external network call is enabled.

Security updates are applied to the latest release and the `main` branch.
