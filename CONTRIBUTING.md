# Contributing to Kates

Thank you for your interest in contributing to Kates (Kafka Advanced Testing & Engineering Suite). This guide will help you get started.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Project Structure](#project-structure)
- [Making Changes](#making-changes)
- [Commit Conventions](#commit-conventions)
- [Pull Request Process](#pull-request-process)
- [Coding Standards](#coding-standards)
- [Testing](#testing)
- [Documentation](#documentation)
- [License](#license)

## Code of Conduct

This project follows a simple principle: be respectful and constructive. We welcome contributors of all experience levels. If you are unsure about anything, open an issue and ask — we are happy to help.

## Getting Started

1. **Fork the repository** and clone your fork locally.
2. **Read the docs** at `kates/docs/` to understand the system architecture.
3. **Pick an issue** labelled `good first issue` or `help wanted`, or open one to discuss your idea.

## Development Setup

### Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Go | 1.23+ | CLI development |
| Java | 21+ | Backend (Quarkus) |
| Maven | 3.9+ (wrapper included) | Backend build |
| Docker | 24+ | Container images |
| Kind | 0.20+ | Local Kubernetes cluster |
| kubectl | 1.28+ | Cluster management |

### Quick Start

```bash
git clone https://github.com/<your-fork>/klster.git
cd klster

# Backend
cd kates
./mvnw clean verify

# CLI
cd ../cli
go build -o kates .
go test ./...
```

### Running Locally

The project includes a `Makefile` at the root for orchestrating the full stack:

```bash
make dev          # spin up Kind cluster + deploy everything
make port-forward # expose services locally
```

Alternatively, run the backend in Quarkus dev mode for rapid iteration:

```bash
cd kates
./mvnw quarkus:dev
```

## Project Structure

```
klster/
├── cli/                    # Go CLI (cobra + lipgloss)
│   ├── cmd/                # Command implementations
│   ├── client/             # API client + types
│   └── output/             # Terminal formatting utilities
├── kates/                  # Java backend (Quarkus)
│   ├── src/main/java/      # Application code
│   │   └── com/klster/kates/
│   │       ├── api/        # REST resources
│   │       ├── domain/     # Domain models
│   │       ├── engine/     # Test orchestration
│   │       ├── service/    # Business logic
│   │       └── webhook/    # Webhook notifications
│   └── docs/               # Documentation book
├── infra/                  # Infrastructure scripts
│   ├── kind/               # Kind cluster config
│   ├── k8s/                # Kubernetes manifests
│   └── scripts/            # Shell utilities
└── Makefile                # Top-level orchestration
```

## Making Changes

1. **Create a branch** from `main` with a descriptive name:
   ```bash
   git checkout -b feat/add-webhook-filtering
   git checkout -b fix/benchmark-timeout
   git checkout -b docs/webhook-guide
   ```

2. **Make your changes** following the [coding standards](#coding-standards).

3. **Write or update tests** to cover your changes.

4. **Verify everything builds and passes:**
   ```bash
   # Backend
   cd kates && ./mvnw clean verify

   # CLI
   cd cli && go build -o kates . && go test ./...
   ```

5. **Commit** using [conventional commits](#commit-conventions).

## Commit Conventions

We use [Conventional Commits](https://www.conventionalcommits.org/) to keep the history clean and enable automated changelog generation.

```
<type>(<scope>): <description>

[optional body]
```

### Types

| Type | When to Use |
|------|-------------|
| `feat` | New feature or capability |
| `fix` | Bug fix |
| `docs` | Documentation only |
| `refactor` | Code change that neither fixes a bug nor adds a feature |
| `test` | Adding or updating tests |
| `chore` | Build, CI, or tooling changes |
| `perf` | Performance improvement |

### Scopes

| Scope | Area |
|-------|------|
| `cli` | Go CLI commands, client, or output |
| `backend` | Java backend (API, engine, services) |
| `infra` | Infrastructure scripts and manifests |
| `docs` | Documentation |

### Examples

```
feat(cli): add webhook management commands
fix(backend): handle null spec in replay endpoint
docs: update cli-reference with gate command
refactor(cli): extract grading logic into shared function
test(backend): add webhook delivery unit tests
```

## Pull Request Process

1. **Ensure CI passes** — all tests, lints, and builds must be green.
2. **Fill out the PR template** with a clear description of what and why.
3. **Keep PRs focused** — one feature or fix per PR. Large changes should be broken into a series of smaller PRs.
4. **Respond to review feedback** promptly and push fixes as additional commits (we squash on merge).
5. **Update documentation** if your change adds or modifies user-facing behaviour.

### PR Checklist

- [ ] Code compiles without warnings
- [ ] Tests pass (`go test ./...` and `./mvnw verify`)
- [ ] New features have documentation in `kates/docs/`
- [ ] CLI help text is updated if commands changed
- [ ] Commit messages follow conventional commit format

## Coding Standards

### Go (CLI)

- Follow standard `gofmt` formatting.
- Use the `output` package for all terminal formatting — never raw `fmt.Printf` with ANSI codes in command files.
- Commands go in `cli/cmd/`, one file per command or logical group.
- API types go in `cli/client/types.go`, API methods in `cli/client/client.go`.
- Use `cmdErr()` for user-facing errors, not `fmt.Errorf`.
- All user-visible strings should be clear and concise.

### Java (Backend)

- Follow standard Java conventions with Quarkus idioms.
- Use constructor injection over field injection.
- Domain logic goes in `domain/` or `service/`, not in REST resources.
- REST resources should be thin — validate input, delegate to services, return responses.
- Use `@ApplicationScoped` for services, `@RequestScoped` only when truly needed.
- Log at appropriate levels: `DEBUG` for internal flow, `INFO` for lifecycle events, `WARN` for recoverable issues.

### General

- No commented-out code in commits.
- No placeholder implementations — if a feature is not ready, do not merge it.
- Prefer small, focused functions over large methods.

## Testing

### Backend

```bash
cd kates
./mvnw clean verify              # full build + tests
./mvnw test                      # tests only
./mvnw quarkus:dev               # dev mode with live reload
```

### CLI

```bash
cd cli
go test ./...                    # all tests
go test ./cmd/ -run TestGate     # specific test
go test ./... -v                 # verbose output
```

### Live Verification

For end-to-end testing against a running cluster:

```bash
./kates health                   # verify connectivity
./kates doctor                   # full pre-flight check
./kates test create --type LOAD --records 10000
./kates benchmark --records 50000
```

## Documentation

All user-facing documentation lives in `kates/docs/` and is structured as a book:

- **Update `cli-reference.md`** when adding or modifying CLI commands.
- **Update `SUMMARY.md`** when adding new pages.
- **Write in a narrative style** — the docs should read like book chapters, not API reference tables.
- **Include examples** — every command section should have copy-pasteable usage examples.
- **Use Mermaid diagrams** for architecture and flow visualisation where helpful.

## License

By contributing to Kates, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).

All new files should include the following header comment where appropriate:

```
Copyright 2026 Kates Contributors
SPDX-License-Identifier: Apache-2.0
```
