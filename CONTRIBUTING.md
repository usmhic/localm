# Contributing to LocalM

Thank you for helping make local inference easier and safer. LocalM is a small
gateway, so focused changes and explicit compatibility behavior are more useful
than broad frameworks or abstractions.

Repository-wide conventions live in [STANDARDS.md](STANDARDS.md). Coding agents
must also follow [AGENTS.md](AGENTS.md).

## Before starting

- Search existing issues and pull requests.
- Open an issue or discussion before large changes, new dependencies, provider
  adapters, or changes to the gateway boundary.
- Keep pull requests focused. Separate unrelated formatting or cleanup.
- Preserve existing environment variables and compatible request/response
  fields unless a documented security issue requires a breaking change.
- Never use production data to reproduce a bug.

## Development setup

Use the Go version pinned in `mise.toml` when possible:

```bash
mise install
go mod download
cp .env.example .env
```

Set a new local-only value in `API_KEYS`. The copied `.env` is ignored. The
default provider is Ollama at `http://127.0.0.1:11434/v1`; an inference runtime
is not needed for unit tests.

To exercise named connections, copy `localm.example.yaml` to the ignored
`localm.yaml`. Reference upstream credentials through `api_key_env`; never put
their values in YAML.

Run the server from source:

```bash
go run .
```

Or use the current checkout in Compose:

```bash
docker compose up --build
```

## Required checks

Run the checks that CI runs before requesting review:

```bash
gofmt -w *.go
go mod tidy
go mod verify
go test -race ./...
go vet ./...
go build ./...
docker compose --no-interpolate config --quiet
docker build -t localm:test .
```

After `go mod tidy`, inspect `go.mod` and `go.sum`; unrelated dependency changes
should not be included. If Gitleaks is installed, scan both history and the
working tree:

```bash
gitleaks git --redact .
gitleaks dir --redact .
```

Use `--redact` so a finding is not copied into terminal logs or CI output.

## Testing expectations

Every behavior change needs a focused test. Depending on the change, cover:

- legacy environment-variable compatibility;
- strict connection configuration and remote trust policy;
- model and capability selection;
- authentication, allowlists, limits, and concurrency;
- synchronous and streaming cancellation/error paths;
- tool, structured-output, reasoning, and multimodal field preservation;
- Responses and embeddings request pass-through;
- output/log redaction and normalized upstream errors.

Provider changes should prefer a preset over an adapter when the runtime already
has an OpenAI-compatible API. Do not add provider SDKs for compatible HTTP
servers.

## Documentation and compatibility

Update all affected public surfaces in the same pull request:

- embedded OpenAPI in `docs.go`;
- `.env.example` and `docs/configuration.md` for environment changes;
- `localm.example.yaml` for connection schema changes;
- architecture, security, provider, or deployment guidance as applicable;
- tests demonstrating preserved legacy behavior.

The default single-Ollama setup must remain minimal.

## Security and privacy

Do not commit, paste into issues, or include in test fixtures:

- real or revoked API keys, tokens, cookies, or authorization headers;
- `.env`, private connection files, certificates, or private keys;
- prompts, responses, embeddings, tool arguments, or customer data;
- internal hostnames, routable private infrastructure URLs, or private email
  addresses;
- unsanitized logs, traces, screenshots, packet captures, or crash dumps.

Use obvious placeholders such as `example.com`, documentation IP ranges, and
values explicitly labeled as non-secret test data. A secret remains compromised
after deletion from the latest commit because Git retains history: rotate it
first, then follow the private process in [SECURITY.md](SECURITY.md).

## Pull requests

A reviewable pull request includes:

- the problem and smallest useful solution;
- tests and exact commands run;
- compatibility and security impact;
- documentation/OpenAPI changes;
- any check that could not run locally and why;
- no new dependency unless its value outweighs maintenance and supply-chain
  cost.

Use short imperative commit subjects, such as `Add capability-aware routing`.
All contributions are accepted under the project's MIT License and must follow
the [Code of Conduct](CODE_OF_CONDUCT.md).
