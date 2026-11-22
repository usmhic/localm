# Changelog

All notable changes to localm are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and releases use
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Optional web research, `POST /v1/search`: up to five queries on a self-hosted
  SearXNG, results merged round-robin and de-duplicated, and the top pages read
  as plain text. Page reads are public-address-only (checked at dial time,
  redirects re-checked), on ports 80/443, with size, time and type limits.
  Queries and pages are never logged or stored.
- Failover: a connection that can't be reached or answers 429/5xx before
  anything was relayed is skipped for the next eligible one (`FAILOVER`).
- Failing connections are paused, from `FAILURE_PAUSE` doubling to
  `FAILURE_PAUSE_MAX`, and tried after every other candidate.
- A bounded queue for busy slots (`MAX_QUEUED_LLM`, `QUEUE_TIMEOUT`).
- `/v1/capabilities` reports the current load, connection pauses and whether
  search is enabled.
- A local stack (`deploy/stack`, `scripts/stack.sh`): Ollama, SearXNG and
  LocalM on one machine, with model sizing, checks and an optional Cloudflare
  tunnel.
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
