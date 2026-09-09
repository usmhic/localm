# Package Naming

This repository follows the shared usmhic package-naming policy in `STANDARDS.md`.
Names below are public identifiers and should be treated as compatibility surfaces.

## Existing names

| Surface | Public name | Rule |
| --- | --- | --- |
| Repository | `localm` | Lowercase product/repository name |
| Go module | `github.com/usmhic/localm` | Canonical module and import path |
| Go executable package | `main` | Intentional single-binary package |
| Container image | `ghcr.io/usmhic/localm` | Owner/repository image path |

## Rules for new code

- Keep Go package names lowercase, short, and without underscores or hyphens.
- Keep the root executable in `package main`; introduce a subpackage only when it creates a real reusable boundary.
- Use `github.com/usmhic/localm/<name>` for new importable subpackages.
- Keep the module path and container image aligned with the GitHub owner and repository name.
- Do not rename the module or executable package without a migration plan and release-note entry.
