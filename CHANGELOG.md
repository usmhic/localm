# Changelog

All notable changes to localm are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and releases use
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

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

- Aligned container, Compose, documentation, dependency-update, and ownership
  metadata with the usmhic open-source ecosystem.
- Removed client IPs and raw relay errors from request logs and connection counts
  from public readiness responses.
- Hardened the container/Compose defaults, ignored local key and data files, and
  expanded the safe environment template and contribution guidance.
