# Architecture and scaling

## Request flow

```text
client
  │ HTTPS + localm bearer key
  ▼
router → CORS → authentication → key/IP limits → concurrency limit
  │
  ├─ model allowlist and request-size/token validation
  ▼
provider interface
  ▼
OpenAI-compatible local inference server
```

The public API layer owns access policy. The provider layer owns only upstream
transport: readiness, authentication to the runtime, chat forwarding, and
stream relay. Unknown compatible JSON fields pass through, which keeps provider
features from leaking into the core gateway.

## Design boundaries

- `main.go`: configuration, HTTP API, middleware, limits, and lifecycle.
- `provider.go`: provider contract, presets, URL validation, and streaming relay.
- `docs.go`: public Swagger UI and embedded OpenAPI document.
- `main_test.go`: endpoint, security, streaming, and pass-through behavior.

The server is stateless except for in-memory rate-limit windows and active
request slots. It stores no prompts, completions, credentials, or model data.

## Scaling

Run multiple replicas behind a reverse proxy when one gateway process is not
enough. Requests can go to any replica. Keep these details in mind:

- Concurrency and rate limits are per replica.
- Use an edge or distributed limiter when limits must be global.
- Scale the inference runtime independently from the gateway.
- Use one gateway deployment per trust boundary or provider.
- Readiness fails when the upstream `/v1/models` endpoint is unavailable.
- Graceful `SIGTERM` handling lets orchestrators drain in-flight requests.

## Adding a provider

Prefer the generic OpenAI-compatible transport. A new preset normally needs
only a normalized name and default URL in `resolveUpstreamBaseURL`.

Add a new `LLMProvider` implementation only when a runtime cannot offer a
compatible `/v1/models` and `/v1/chat/completions` contract. Keep runtime-specific
translation inside that implementation and add synchronous, streaming, error,
and tool-call tests.
