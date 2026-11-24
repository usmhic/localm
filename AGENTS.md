# Repository guide for coding agents

## Product

localm is a small, secure OpenAI-compatible gateway for local LLM runtimes,
maintained by usmhic. It is a gateway, not a model manager, search engine, or agent runtime: it
only puts authentication, limits, and routing in front of models. Orchestration,
tools, and web research belong to the clients.

## Map

- `main.go`: HTTP surface, auth, limits, queueing, routing, failover, and startup.
- `config.go`: environment and named-connections configuration.
- `capabilities.go`: capability vocabulary, discovery cache, and connection pauses.
- `provider.go`: provider presets and upstream URL normalization.
- `docs.go`: embedded OpenAPI and Swagger assets.
- `docs`: focused operator, provider, deployment, laptop, and agent guides.
- `docker-compose.yml` (+ `docker-compose.linux.yml`), `scripts/serve.sh`: serving a
  machine's native Ollama through LocalM, with an optional Cloudflare tunnel.

Read `docs/architecture.md` before expanding the API surface.

## Commands

- Format: `gofmt -w *.go`
- Dependencies: `go mod download && go mod verify`
- Test: `go test -race ./...`
- Static analysis: `go vet ./...`
- Build: `go build ./...`
- Compose: `docker compose --no-interpolate config --quiet` and
  `docker compose -f docker-compose.yml -f docker-compose.linux.yml config --no-interpolate --quiet`
- Container: `docker build -t localm:test .`

## Guardrails

- Preserve OpenAI-compatible request and response fields, including unknown extensions.
- Never log API keys, private prompts, tool arguments, or model output.
- Never log client IPs, upstream URLs, authorization headers, raw upstream
  errors, or connection secrets.
- Keep streaming cancellation and error behavior covered by tests.
- Prefer standard-library solutions and provider presets over adapters.
- Update embedded OpenAPI and docs with every public API/configuration change.
- Keep defaults safe for a gateway exposed beyond localhost.
- Fail over only before the first relayed byte, and never retry a request the
  upstream rejected (4xx other than 429).
- Keep the project generic: no product-specific names, defaults, or behavior.
- Keep the setup small: a working gateway needs only `API_KEYS` and
  `ALLOWED_MODELS`; new settings get safe defaults and live in
  `docs/configuration.md`, not in the short `.env.example`.
