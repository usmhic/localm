# Contributing to localm

Thank you for helping make local inference easier and safer.

Repository-wide conventions live in [STANDARDS.md](STANDARDS.md), and the
concise working map for coding agents lives in [AGENTS.md](AGENTS.md).

## Before you start

- Search existing issues before opening a new one.
- Use an issue or discussion to align on large changes.
- Keep pull requests focused; unrelated cleanup is easier to review separately.
- Never include real API keys, private prompts, or model output containing
  sensitive data.

## Local development

Requirements: Go 1.25 or newer and, for integration testing, Docker plus one
supported local inference runtime.

```bash
cp .env.example .env
go test -race ./...
go vet ./...
go run .
```

Format changed Go files with `gofmt -w`. Build the production image with:

```bash
docker build -t localm:test .
```

## Pull requests

A good pull request includes:

- A clear problem statement and a small, reviewable solution.
- Tests for behavior changes, including error and streaming paths where relevant.
- Documentation and OpenAPI updates for public API or configuration changes.
- Backward compatibility notes for renamed settings or changed defaults.
- No new dependency unless its value outweighs its maintenance and supply-chain
  cost.

Provider changes should test request pass-through, upstream errors, streaming,
and tool-call payloads. Prefer adding a provider preset over a new adapter when
the runtime already exposes an OpenAI-compatible API.

## Commit and review style

Use short imperative commit subjects, such as `Add LM Studio provider preset`.
Be kind and specific in review. It is fine to ask for help or submit a draft.

All contributions are accepted under the project’s MIT License and must follow
the [Code of Conduct](CODE_OF_CONDUCT.md).
