# Architecture and scaling

## Scope

LocalM is a stateless LLM gateway. It authenticates and constrains client
requests, selects an explicitly configured OpenAI-compatible connection, and
relays compatible responses. It can also answer web research requests from a
self-hosted search engine (`/v1/search`), a retrieval primitive like
embeddings. Model installation/loading, chats, tool execution, workflows, and
agent state remain outside the process: LocalM returns sources, it never
decides what to do with them.

## Request flow

```text
client
  | HTTPS + LocalM bearer key
  v
auth -> key/IP limits -> concurrency slot (or bounded queue) -> body/token/model validation
  v
derive required capabilities from endpoint and request fields
  v
order known-available connections by capability and descending priority,
paused (recently failing) connections last
  v
generic OpenAI-compatible transport; on a connection failure, 429 or 5xx
before any byte is relayed, the next candidate
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
highest priority first, then connection name.

With `FAILOVER` (on by default), a connection that can't be reached or answers
429 or 5xx is skipped for the next candidate, as long as nothing has been
relayed to the client: the request body is resent unchanged and the client sees
one answer. Once a response has started (including a stream's first byte), it
is final. A connection that failed is paused for `FAILURE_PAUSE`, doubling per
consecutive failure up to `FAILURE_PAUSE_MAX`; paused connections are tried
after every other candidate, so a sole connection is never locked out, and a
success clears the pause. Requests that LocalM rejected (4xx other than 429) are
never retried.

When every concurrency slot is busy, up to `MAX_QUEUED_LLM` requests wait up to
`QUEUE_TIMEOUT` for one; beyond that LocalM answers 429 at once, so a client
with another backend can go there instead of waiting.

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

The server stores only in-memory rate windows, active and queued request slots,
connection pauses, and the short-lived discovery cache. It stores no prompts, completions, embeddings,
credentials, or model data.

Run replicas behind a reverse proxy when needed. Rate limits, concurrency, and
discovery caches are per replica. Use an edge/distributed limiter when limits
must be global, and scale inference connections independently.

## Code boundaries

- `config.go`: legacy environment compatibility and strict YAML connections.
- `capabilities.go`: capability vocabulary, presets, and model discovery cache.
- `main.go`: HTTP surface, policy enforcement, queueing, routing, failover, and
  lifecycle.
- `search.go`: the optional `/v1/search` primitive, SearXNG queries, result
  merging, and public-address-only page reading.
- `provider.go`: generic compatible transport, model parsing, streaming, and
  sanitized upstream errors.
- `docs.go`: embedded OpenAPI and Swagger UI.

## Web research

`/v1/search` stays a retrieval primitive: queries in, sources out. Page reads
are the only requests LocalM makes to addresses it did not configure, so they
use a separate HTTP client that refuses non-public addresses when connecting
(after DNS resolution), re-checks every redirect, ignores environment proxies,
and bounds ports, size, time and content type. See [Web research](search.md).

## Local stack

`deploy/stack` composes Ollama, SearXNG and LocalM (plus an optional tunnel) on
one machine, with LocalM as the only published port. It is a deployment
recipe, not part of the binary. See [Local stack](local-stack.md).

Prefer a provider preset over a provider-specific adapter. Add an adapter only
when a runtime cannot expose compatible models and inference endpoints.
