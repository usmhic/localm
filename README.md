# localm

> Tired of the “token expired” message appearing exactly when your agent finally
> starts doing useful work?

What if you could run a model on your own GPU, keep using OpenAI-compatible
clients, and expose only a tiny guarded API instead of your raw inference
server? That is `localm`: a small, secure gateway between your apps and the
local LLM runtime you already like.

[![CI](https://github.com/usmhic/localm/actions/workflows/ci.yml/badge.svg)](https://github.com/usmhic/localm/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## Why localm?

- One OpenAI-compatible URL for Ollama, LM Studio, llama.cpp, LocalAI, or a
  custom compatible server.
- Streaming chat completions and `/v1/models` for existing SDKs and UIs.
- Agent-ready pass-through for tools, tool calls, structured output, multimodal
  messages, and provider-specific extensions.
- API-key authentication, model allowlists, request limits, rate limits, and
  concurrency controls in front of local inference.
- Public Swagger UI at `/docs/` without making the model endpoint public.
- A static, non-root container that is comfortable on Docker, GHCR, and
  Dokploy.
- No runtime dependencies and no telemetry.

## Five-minute start

You need Docker and [Ollama](https://ollama.com/).

```bash
ollama run qwen3:8b
cp .env.example .env
```

Set `API_KEYS` in `.env` to a long random key, then run:

```bash
docker compose up -d
```

Compose pulls `ghcr.io/usmhic/localm:prod`, the image published from `main`.
Set `LOCALM_IMAGE_TAG=dev` in `.env` to follow development builds instead.

Open <http://localhost:8080/docs/> or send a request:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ${LOCALM_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3:8b","messages":[{"role":"user","content":"Why is local inference fun?"}]}'
```

That is the whole required setup. The raw OpenAPI document lives at
<http://localhost:8080/openapi.json>.

For source development, `mise install` provides the Go version used by CI and
the production image; the module remains compatible with Go 1.25 or newer.

## Choose your local runtime

`ollama` is the default. To use another runtime, set two values:

```dotenv
LLM_PROVIDER=lmstudio
UPSTREAM_BASE_URL=http://host.docker.internal:1234/v1
```

| Runtime | `LLM_PROVIDER` | Typical base URL |
| --- | --- | --- |
| Ollama | `ollama` | `http://localhost:11434/v1` |
| LM Studio | `lmstudio` | `http://localhost:1234/v1` |
| llama.cpp | `llamacpp` | `http://localhost:8080/v1` |
| LocalAI | `localai` | `http://localhost:8080/v1` |
| Any compatible API | `custom` | Your server’s `/v1` URL |

The runtime can serve multiple models; list the ones clients may use with
`ALLOWED_MODELS=model-a,model-b`. See [Provider setup](docs/providers.md) for
copy-paste configurations and Docker networking notes.

## Use it with an agent

Point an OpenAI-compatible agent or SDK at `http://localhost:8080/v1` and use
your `localm` key. Tool definitions and tool-call messages pass through to the
selected runtime unchanged.

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key=os.environ["LOCALM_API_KEY"],
)

response = client.chat.completions.create(
    model="qwen3:8b",
    messages=[{"role": "user", "content": "Say hello from my GPU."}],
)
print(response.choices[0].message.content)
```

See [Agent and tool-calling guide](docs/agents.md) for a complete local tool
loop and runtime-specific notes.

## API surface

| Endpoint | Auth | Purpose |
| --- | --- | --- |
| `POST /v1/chat/completions` | Bearer key | Chat, streaming, tools, and compatible extensions |
| `GET /v1/models` | Bearer key | Models exposed by `ALLOWED_MODELS` |
| `GET /healthz` | Public | Process liveness |
| `GET /readyz` | Public | Upstream model-server readiness |
| `GET /docs/` | Public | Interactive Swagger UI |
| `GET /openapi.json` | Public | OpenAPI document |

`localm` intentionally stays focused: it is an access gateway, not a model
manager and not an agent runtime. Your chosen inference server loads models;
your application executes tools.

## Configuration

Only `API_KEYS` is required for the default Docker Compose setup.

| Variable | Default | Description |
| --- | --- | --- |
| `API_KEYS` | required | Comma-separated client keys |
| `ALLOWED_MODELS` | `qwen3:8b` | Comma-separated model allowlist |
| `LLM_PROVIDER` | `ollama` | Provider preset |
| `UPSTREAM_BASE_URL` | provider default | OpenAI-compatible `/v1` URL |
| `UPSTREAM_API_KEY` | empty | Optional key for the upstream server |
| `MAX_TOKENS` | `1024` | Maximum accepted output-token request |
| `DEFAULT_MAX_TOKENS` | `512` | Added when the client omits a token limit |
| `MAX_CONCURRENT_LLM` | `4` | Concurrent upstream requests per replica |
| `RATE_LIMIT_PER_KEY_RPM` | `60` | Requests per key per minute, per replica |
| `RATE_LIMIT_PER_IP_RPM` | `120` | Requests per IP per minute, per replica |

Advanced timeout, body-size, listener, and CORS settings are documented in
[Configuration](docs/configuration.md). `OLLAMA_BASE_URL` and
`MAX_CONCURRENT_OLLAMA` remain supported as compatibility aliases.

## Architecture and deployment

```text
OpenAI client / agent
        │
        ▼
 localm ── auth · limits · allowlist · streaming
        │
        ▼
Ollama / LM Studio / llama.cpp / LocalAI / custom API
        │
        ▼
     local CPU or GPU
```

- [Architecture and scaling](docs/architecture.md)
- [Docker, GHCR, and Dokploy deployment](docs/deployment.md)
- [Engineering standards](STANDARDS.md)
- [Coding-agent guide](AGENTS.md)
- [Security policy](SECURITY.md)

CI runs formatting, race-enabled tests, static analysis, and a production image
build. Pushes to `dev` and `main` publish multi-architecture `dev` and
`prod`/`latest` images; `vX.Y.Z` tags create GitHub releases.

## Contributing

Small fixes, provider compatibility reports, tests, and documentation
improvements are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md), open an
issue, or start a discussion before a large change.

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

MIT — see [LICENSE](LICENSE). Maintained by [usmhic](https://github.com/usmhic).
