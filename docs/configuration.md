# Configuration reference

Values are read once at startup. Invalid connection files, URLs, trust policy,
or missing required keys stop startup. Malformed optional numeric environment
values retain their existing behavior and fall back to safe defaults.

## Environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `API_KEYS` | required | Comma-separated keys accepted from clients |
| `ALLOWED_MODELS` | `qwen3:8b` | Global model allowlist; connection models must be a subset |
| `LOCALM_CONFIG` | empty | Optional named-connections YAML path |
| `LLM_PROVIDER` | `ollama` | Legacy mode: `ollama`, `lmstudio`, `llamacpp`, `localai`, or `custom` |
| `UPSTREAM_BASE_URL` | provider preset | Legacy mode: absolute compatible `/v1` URL |
| `UPSTREAM_API_KEY` | empty | Legacy mode: bearer key sent only upstream |
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `MAX_BODY_BYTES` | `1048576` | Maximum request body size |
| `MAX_TOKENS` | `1024` | Maximum requested output tokens |
| `DEFAULT_MAX_TOKENS` | `512` | Limit inserted when a generation request omits one |
| `RATE_LIMIT_PER_KEY_RPM` | `60` | Per-key requests per minute per replica |
| `RATE_LIMIT_PER_IP_RPM` | `120` | Per-IP requests per minute per replica |
| `MAX_CONCURRENT_LLM` | `4` | Simultaneous upstream requests per replica |
| `UPSTREAM_TIMEOUT` | `120s` | Whole upstream inference timeout |
| `READINESS_TIMEOUT` | `3s` | Total discovery/readiness request timeout |
| `CAPABILITY_DISCOVERY_TTL` | `30s` | In-memory model discovery cache lifetime |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |
| `SERVER_READ_TIMEOUT` | `15s` | HTTP request read timeout |
| `SERVER_WRITE_TIMEOUT` | `180s` | HTTP response write timeout |
| `SERVER_IDLE_TIMEOUT` | `60s` | Keep-alive idle timeout |
| `CORS_ENABLED` | `false` | Enable browser cross-origin access |
| `CORS_ALLOWED_ORIGINS` | empty | Comma-separated exact origins, or `*` |

`OLLAMA_BASE_URL` remains a compatibility alias when `LLM_PROVIDER=ollama` and
`UPSTREAM_BASE_URL` is absent. `MAX_CONCURRENT_OLLAMA` remains a fallback for
`MAX_CONCURRENT_LLM`. If `LOCALM_CONFIG` is empty, all legacy behavior uses one
connection named `default`.

## Named-connections file

The file is strict YAML: unknown fields fail startup. Upstream keys are never
placed in YAML; `api_key_env` names the environment variable containing a key.

```yaml
allow_remote: true
connections:
  local:
    provider: ollama
    base_url: http://host.docker.internal:11434/v1
    priority: 100
    trust: local
    models: [qwen3:8b]

  remote:
    provider: custom
    base_url: https://example.com/v1
    api_key_env: REMOTE_LLM_API_KEY
    priority: 50
    trust: remote
    models: [qwen3:8b]
    capabilities:
      - chat_completions
      - responses
      - embeddings
      - streaming
```

Connection fields:

| Field | Default | Meaning |
| --- | --- | --- |
| `provider` | `ollama` | Provider preset |
| `base_url` | provider preset | Absolute HTTP(S) compatible `/v1` URL |
| `api_key_env` | empty | Environment variable holding the upstream bearer key |
| `priority` | `0` | Higher values route first |
| `trust` | `local` | `local` or `remote`; non-local URLs require `remote` |
| `models` | all `ALLOWED_MODELS` | Models this connection may serve |
| `capabilities` | endpoint preset | Complete replacement capability profile |

Remote connections require both `trust: remote` and top-level
`allow_remote: true`. Loopback/private IPs, localhost, single-label container
service names, and common `.local`, `.internal`, and cluster service suffixes
are classified as local. Other private DNS names should use the explicit remote
policy because LocalM does not perform DNS lookups during configuration.

Valid capability names are `chat_completions`, `responses`, `embeddings`,
`streaming`, `tools`, `structured_output`, `reasoning`, and `vision`. If
`capabilities` is present it replaces, rather than extends, the preset. Include
the endpoint capability as well as every optional feature intended for that
connection. Presets declare compatible endpoints and streaming only;
model-dependent tools, structured output, reasoning, and vision require YAML
declaration or provider model metadata. Legacy environment mode keeps its
historical pass-through behavior.

## Key rotation

Place the old and new client keys in `API_KEYS`, deploy, move clients to the new
key, then remove the old key and deploy again. Rotate upstream keys by changing
the environment variable referenced by `api_key_env` and restarting LocalM.

Do not commit `.env`; it is ignored by Git and the Docker build context.
The tracked `.env.example` contains empty values for every application and
Compose setting. Empty optional values retain the documented defaults.
