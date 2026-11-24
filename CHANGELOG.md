# Changelog

All notable changes to localm are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and releases use
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Failover: a connection that can't be reached or answers 429/5xx before
  anything was relayed is skipped for the next eligible one (`FAILOVER`).
- Failing connections are paused, from `FAILURE_PAUSE` doubling to
  `FAILURE_PAUSE_MAX`, and tried after every other candidate.
- A bounded queue for busy slots (`MAX_QUEUED_LLM`, `QUEUE_TIMEOUT`).
- `/v1/capabilities` reports the current load and connection pauses.
- Serving a laptop's models (`scripts/serve.sh`, docs/laptop.md): native Ollama
  for the GPU, LocalM in front through Docker Compose (host networking on
  Linux), an optional Cloudflare tunnel, and limits sized for agents.
- A short `.env.example`: `API_KEYS` and `ALLOWED_MODELS` are all a working
  gateway needs; every other setting is in docs/configuration.md.
- Optional strict YAML configuration for multiple named connections with
  explicit local/remote trust, model subsets, capability profiles, and priority
  routing.
- OpenAI-compatible `/v1/responses`, `/v1/embeddings`, and authenticated
  `/v1/capabilities` endpoints.
- Lazy, cached provider/model discovery and sanitized capability reporting.
- Capability-aware validation for streaming, tools, structured output, and
  reasoning fields, plus normalized upstream errors.
- Shared engineering standards and coding-agent guidance.
- Tag-driven GitHub release automation.
- A mise toolchain pin matching CI and the production builder.
- Secret-history scanning, dependency integrity checks, Compose validation,
  container smoke tests, and CI-gated image publication.

### Changed

- Added a documented package-naming contract for the Go module, executable package, and container image.
- Aligned container, Compose, documentation, dependency-update, and ownership
  metadata with the usmhic open-source ecosystem.
- Removed client IPs and raw relay errors from request logs and connection counts
  from public readiness responses.
- Hardened the container/Compose defaults, ignored local key and data files, and
  expanded the safe environment template and contribution guidance.
