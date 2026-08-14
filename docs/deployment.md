# Deployment

## Docker Compose

```bash
cp .env.example .env
docker compose up -d
curl http://localhost:8080/healthz
```

Compose always pulls `ghcr.io/usmhic/localm:prod`, which is published by the
`main` workflow. Set `LOCALM_IMAGE_TAG=dev` in `.env` to use the image published
by the `dev` workflow. Compose binds `8080` to localhost by default. Put an HTTPS
reverse proxy or secure tunnel in front of the gateway before remote access.
The GHCR package must be public; otherwise run `docker login ghcr.io` before
starting Compose.

## GHCR

The repository publishes development and production images:

```bash
docker pull ghcr.io/usmhic/localm:dev
docker pull ghcr.io/usmhic/localm:prod
```

Immutable commit tags use `dev-<sha>` and `prod-<sha>`. `latest` follows
`main`.

## Dokploy

For a source deployment, create a Dokploy Application:

| Setting | Value |
| --- | --- |
| Build type | `Dockerfile` |
| Dockerfile path | `Dockerfile` |
| Docker context path | `.` |
| Docker build stage | empty (final `runtime` stage) |
| Domain path | `/` |
| Container port | `8080` |

Set at least:

```dotenv
API_KEYS=
UPSTREAM_BASE_URL=http://a-host-reachable-from-the-container:11434/v1
ALLOWED_MODELS=qwen3:8b
```

The upstream address must be reachable from inside the deployed container;
`127.0.0.1` points back to `localm`. Configure `/healthz` as the process health
check and `/readyz` when deployment health should depend on model readiness.

For a prebuilt-image deployment, use `ghcr.io/usmhic/localm:prod` and the same
container port and environment configuration.

## Production checklist

- Terminate TLS at a trusted reverse proxy.
- Generate unique, high-entropy client keys and store them as secrets.
- Keep the raw inference server on a private network.
- Use a narrow model allowlist.
- Set CPU, memory, and replica limits in the orchestrator.
- Send logs to a protected sink; prompts and responses are not logged.
- Monitor `/healthz`, `/readyz`, latency, HTTP 429s, and upstream failures.
- Pin an immutable `prod-<sha>` image when reproducibility matters.
