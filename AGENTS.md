# Repository guide for coding agents

## Product

localm is a small, secure OpenAI-compatible gateway for local LLM runtimes,
maintained by usmhic. It is a gateway, not a model manager or agent runtime.

## Map

- `main.go`: configuration, HTTP surface, auth, limits, streaming, and startup.
- `provider.go`: provider presets and upstream URL normalization.
- `docs.go`: embedded OpenAPI and Swagger assets.
- `docs`: focused operator, provider, deployment, and agent guides.

Read `docs/architecture.md` before expanding the API surface.

## Commands

- Format: `gofmt -w *.go`
- Test: `go test -race ./...`
- Static analysis: `go vet ./...`
- Build: `go build ./...`
- Container: `docker build -t localm:test .`

## Guardrails

- Preserve OpenAI-compatible request and response fields, including unknown extensions.
- Never log API keys, private prompts, tool arguments, or model output.
- Keep streaming cancellation and error behavior covered by tests.
- Prefer standard-library solutions and provider presets over adapters.
- Update embedded OpenAPI and docs with every public API/configuration change.
- Keep defaults safe for a gateway exposed beyond localhost.
