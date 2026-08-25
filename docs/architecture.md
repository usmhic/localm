# Architecture and scaling

## Scope

LocalM is a stateless LLM gateway. It authenticates and constrains client
requests, selects an explicitly configured OpenAI-compatible connection, and
relays compatible responses. Model installation/loading, chats, tool execution,
workflows, and agent state remain outside the process.

## Request flow

```text
client
  | HTTPS + LocalM bearer key
  v
auth -> key/IP limits -> concurrency limit -> body/token/model validation
  v
derive required capabilities from endpoint and request fields
  v
select a known-available model connection by capability and descending priority
  v
generic OpenAI-compatible transport
  v
Ollama / LM Studio / llama.cpp / LocalAI / vLLM / compatible server
```

The transport preserves unknown compatible request fields and successful
response fields. The gateway validates feature-bearing fields first: streaming,
tools, structured output, reasoning, and image input are never removed to make a request fit
a provider. If no connection declares the feature, the client receives the
normalized `unsupported_feature` error.

Upstream error bodies are not relayed because they may contain internal URLs or
provider details. LocalM maps them to stable OpenAI-style envelopes while
preserving useful HTTP status classes.

## Connections and routing

Legacy environment variables resolve to one connection named `default`. An
optional YAML file resolves to several named connections. Both paths produce
the same `ConnectionConfig` and use the same provider transport.

Each connection has a provider preset, base URL, secret environment-variable
reference, priority, trust class, model subset, and capability set. The global
`ALLOWED_MODELS` remains the outer security boundary. Selection is deterministic:
highest priority first, then connection name. The current phase does not retry
or fail over after an upstream request has begun; those behaviors need explicit
idempotency and streaming rules.

Before discovery, configured connections are eligible so startup remains lazy.
After discovery, routing skips unhealthy connections and connections where the
requested model was not listed until the discovery cache expires.

## Capability discovery

`GET /v1/capabilities` performs a bounded, cached `GET /v1/models` against every
connection. It reports configured allowed models, availability, health, trust,
priority, and effective capabilities. When a provider returns a
`capabilities` or `supported_features` array for a model, that model metadata
conservatively refines optional features. Otherwise the connection profile is
used. Operators can replace preset profiles in YAML.

Discovery never sends inference input and never returns base URLs, credentials,
headers, prompts, or provider error details. It is lazy: no discovery or other
background network traffic occurs merely because LocalM started.

## State and scaling

The server stores only in-memory rate windows, active request slots, and the
short-lived discovery cache. It stores no prompts, completions, embeddings,
credentials, or model data.

Run replicas behind a reverse proxy when needed. Rate limits, concurrency, and
discovery caches are per replica. Use an edge/distributed limiter when limits
must be global, and scale inference connections independently.

## Code boundaries

- `config.go`: legacy environment compatibility and strict YAML connections.
- `capabilities.go`: capability vocabulary, presets, and model discovery cache.
- `main.go`: HTTP surface, policy enforcement, routing, and lifecycle.
- `provider.go`: generic compatible transport, model parsing, streaming, and
  sanitized upstream errors.
- `docs.go`: embedded OpenAPI and Swagger UI.

Prefer a provider preset over a provider-specific adapter. Add an adapter only
when a runtime cannot expose compatible models and inference endpoints.
