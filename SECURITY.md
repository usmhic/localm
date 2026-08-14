# Security policy

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Use GitHub’s
private vulnerability reporting feature on the repository’s **Security** tab.
Include the affected version or commit, reproduction steps, impact, and any
suggested mitigation.

Maintainers will acknowledge a complete report as soon as practical, validate
the issue, coordinate a fix, and credit the reporter unless anonymity is
requested. Please allow time for a patch before public disclosure.

## Deployment assumptions

- `localm` must sit behind HTTPS when accessed over an untrusted network.
- The inference runtime should remain on a private network.
- `/docs/`, `/openapi.json`, `/healthz`, and `/readyz` are intentionally public.
- `/v1/chat/completions` and `/v1/models` require a configured bearer key.
- Client keys and `UPSTREAM_API_KEY` must be managed as deployment secrets.
- Rate limits are in memory and apply per replica, not globally.

Security updates are applied to the latest release and the `main` branch.
