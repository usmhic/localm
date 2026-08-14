# Configuration reference

Values are read once at startup. Invalid provider URLs and missing required
keys stop startup; malformed optional numeric values fall back to their safe
defaults.

| Variable | Default | Description |
| --- | --- | --- |
| `API_KEYS` | required | Comma-separated keys accepted from clients |
| `ALLOWED_MODELS` | `qwen3:8b` | Models clients may request |
| `LLM_PROVIDER` | `ollama` | `ollama`, `lmstudio`, `llamacpp`, `localai`, or `custom` |
| `UPSTREAM_BASE_URL` | provider preset | Absolute OpenAI-compatible `/v1` URL |
| `UPSTREAM_API_KEY` | empty | Bearer key sent only to the upstream API |
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `MAX_BODY_BYTES` | `1048576` | Maximum request body size |
| `MAX_TOKENS` | `1024` | Maximum requested output tokens |
| `DEFAULT_MAX_TOKENS` | `512` | Limit inserted when the request omits one |
| `RATE_LIMIT_PER_KEY_RPM` | `60` | Per-key requests per minute per replica |
| `RATE_LIMIT_PER_IP_RPM` | `120` | Per-IP requests per minute per replica |
| `MAX_CONCURRENT_LLM` | `4` | Simultaneous upstream requests per replica |
| `UPSTREAM_TIMEOUT` | `120s` | Whole upstream request timeout |
| `READINESS_TIMEOUT` | `3s` | Upstream readiness timeout |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |
| `SERVER_READ_TIMEOUT` | `15s` | HTTP request read timeout |
| `SERVER_WRITE_TIMEOUT` | `180s` | HTTP response write timeout |
| `SERVER_IDLE_TIMEOUT` | `60s` | Keep-alive idle timeout |
| `CORS_ENABLED` | `false` | Enable browser cross-origin access |
| `CORS_ALLOWED_ORIGINS` | empty | Comma-separated exact origins, or `*` |

`OLLAMA_BASE_URL` is a compatibility alias used only when `LLM_PROVIDER=ollama`
and `UPSTREAM_BASE_URL` is absent. `MAX_CONCURRENT_OLLAMA` is a compatibility
fallback for `MAX_CONCURRENT_LLM`.

## Key rotation

Place the old and new keys in `API_KEYS`, deploy, move clients to the new key,
then remove the old key and deploy again:

```dotenv
API_KEYS=
```

Do not commit `.env`; it is ignored by Git and the Docker build context.
