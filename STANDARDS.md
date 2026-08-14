# usmhic engineering standards

This repository is part of the [usmhic](https://github.com/usmhic) open-source
ecosystem. These standards are copied into each repository so every checkout is
self-contained. Framework-native practices win when they are more specific;
document intentional exceptions in `ARCHITECTURE.md`.

## Principles

- Keep the default path simple: clone, copy the environment template, and run.
- Prefer explicit names, small modules, typed boundaries, and boring automation.
- Write documentation for a capable newcomer with no private project context.
- Treat people and coding agents as first-class maintainers: record commands,
  constraints, ownership, and architectural decisions in the repository.
- Keep branding light. Use the product name for the product and `usmhic` for
  authorship, source links, package ownership, and ecosystem context.
- Never commit credentials, private customer data, or production configuration.

## Repository baseline

Every public repository includes:

- `README.md` as the entry point;
- `ARCHITECTURE.md` for boundaries and important decisions;
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, and an MIT
  `LICENSE`;
- `CHANGELOG.md` for versioned products;
- `AGENTS.md` with repository-specific guidance;
- `.editorconfig`, `.gitignore`, and a safe `.env.example` when configuration
  exists;
- structured issue forms, a pull request template, Dependabot, CI, and a release
  workflow where releases are meaningful.

A multi-app repository uses framework-native directories such as `api/`,
`web/`, and `mobile/`. A single app stays framework-native instead of gaining
ceremonial layers.

## Naming

| Surface | Convention |
| --- | --- |
| Repositories | lowercase; product name; avoid separators unless readability needs one |
| Branches | `main`, `dev`, `feat/<slug>`, `fix/<slug>`, `docs/<slug>`, `chore/<slug>` |
| Commits | imperative, focused, preferably Conventional Commit style |
| Workflows | `ci.yml` for one app; `<surface>-ci.yml` for multi-app repos; `release.yml` for tags |
| Containers | `ghcr.io/usmhic/<repo>` or `ghcr.io/usmhic/<repo>-<surface>` |
| Environment variables | uppercase snake case; public browser variables use the framework prefix |
| Packages/modules | follow the language and framework convention already used by the project |

External URLs always use the real GitHub owner and repository name. Historical
product spellings or package scopes may remain when renaming would break users;
explain them once in the README.

## Documentation

The root README should answer, in order:

1. What is this and why would I use it?
2. How is it put together?
3. How do I run it quickly?
4. Which commands and configuration matter day to day?
5. How is it tested, built, and released?
6. Where are the deeper docs and contribution rules?

Nested READMEs describe only their surface. Do not duplicate long environment
variable tables in several places; keep the canonical template in
`.env.example` and link to focused docs. Update documentation, examples,
OpenAPI descriptions, and screenshots with the behavior they describe.

## Environment configuration

- Ignore `.env`, `.env.*`, `*.env`, `*.env.*`, `.envrc`, `.direnv/`, `.flaskenv`,
  and `.streamlit/secrets.toml` at every depth. The only exception is the
  sanitized repository-root `.env.example`.
- Keep every value in `.env.example` empty. Explain expected formats in comments
  or documentation instead of committing realistic tokens, passwords, private
  URLs, or runnable dummy credentials.
- Store local, CI, staging, and production values outside Git. Fail loudly when
  a required value is missing.
- Group variables by concern and explain non-obvious values with short comments.
- Add, rename, or remove a template entry in the same change as the code.
- Build-time public values and runtime secrets are different interfaces; never
  bake runtime secrets into images or browser bundles.

## Containers and Compose

Production Dockerfiles use multi-stage builds when the framework benefits,
install from committed lockfiles, run as a non-root user, expose a health check
where practical, and include OCI labels for title, description, source, authors,
and license.

Compose is the local integration contract. Give the project a stable `name:`,
use named volumes, health-check dependencies, keep service names predictable,
and make destructive volume removal an explicit command in documentation.
Images published from `main` receive `latest`; development images receive
`dev`; immutable SHA tags are produced for traceability.

## CI/CD

All workflows:

- request the smallest token permissions they need;
- use concurrency cancellation for superseded checks and builds;
- install from lockfiles;
- run the same checks documented for local development;
- cache through official setup actions where useful;
- never publish images or deploy from pull requests;
- keep release/store submissions non-cancelling when interruption could leave an
  external system half-updated.

Pull requests run formatting or style checks, static analysis/type checks,
tests, and a production build appropriate to the framework. Containerized apps
also build the production image; smoke tests are preferred when they are stable
and reasonably fast.

Dependabot checks GitHub Actions and each committed package ecosystem monthly.
Group safe patch/minor updates when that reduces noise.

## Security and dependencies

Report vulnerabilities privately through GitHub Security Advisories. Remove
secrets, private prompts, customer data, and sensitive output from issues and
logs. Pin toolchains, commit one lockfile per package manager and app, and use
the pinned package manager everywhere. Review generated lockfile changes and
major upgrades deliberately.

Containers drop unnecessary privileges. Public health and documentation routes
must not expose configuration. Production data stores and administration ports
belong on private networks behind TLS-terminating infrastructure.

## GitHub repository settings

Keep the description to one plain sentence aligned with the README, use useful
technology and domain topics, enable private vulnerability reporting, and
enable secret scanning/push protection where GitHub supports them. Protect
`main`; require pull requests and passing checks; squash merge; delete merged
branches automatically. Do not change repository visibility until the full git
history has been checked for secrets.

## Releases

Use Semantic Versioning where the project exposes versions. Keep
`CHANGELOG.md` in Keep a Changelog shape. Tags named `vX.Y.Z` create GitHub
releases; mobile products may use their documented platform-specific flow.
Release automation must be reproducible from committed source and must not hide
required manual credentials.

## Definition of done

A change is done when its focused tests pass, the relevant production build
passes, configuration and public behavior are documented, no secret or
generated local artifact is included, and the diff is understandable without
private context. If a check cannot run locally, state exactly what was skipped
and why.
