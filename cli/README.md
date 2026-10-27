# Kates CLI

Terminal-first client for the Kates backend API. Built in Go with Cobra, featuring rich terminal output with tables, sparklines, colored badges, and ASCII banners.

## Quick Start

```bash
# Build and install
go build -o kates .
mv kates /usr/local/bin/

# Configure
kates ctx set local --url http://localhost:30083
kates ctx use local

# Verify
kates health
```

Or from the project root:

```bash
make cli-install
```

## Command Groups

| Group | Commands | Description |
|-------|----------|-------------|
| **cluster** | `info`, `topics`, `broker configs`, `check`, `groups`, `watch` | Kafka cluster introspection |
| **test** | `create`, `list`, `show`, `delete`, `apply`, `scaffold` | Test execution and lifecycle |
| **report** | `show`, `summary`, `export csv/junit`, `diff`, `brokers` | Results, reports, and export |
| **trend** | `--type`, `--metric`, `--days` | Sparkline trend analysis |
| **disruption** | `create`, `list`, `show`, `watch`, `playbook` | Chaos engineering |
| **schedule** | `create`, `list`, `show`, `delete` | Cron-based recurring tests |
| **ctx** | `set`, `use`, `show`, `current`, `delete` | Multi-context management |
| **dashboard** | — | Full-screen monitoring |
| **top** | — | Live running test status |
| **watch** | `<id>` | Real-time test streaming |
| **health** | — | System health check |
| **status** | — | One-line system status |
| **version** | — | CLI, API, and runtime info |

## Project Structure

```
cli/
├── main.go          Entry point
├── cmd/             Cobra command definitions (29 files)
│   ├── root.go      Root command, context loading, global flags
│   ├── cluster.go   Cluster inspection commands
│   ├── test.go      Test lifecycle commands
│   ├── apply.go     YAML scenario application with SLA validation
│   ├── scaffold.go  Test template generation
│   ├── report.go    Report display and SLA verdicts
│   ├── trend.go     Sparkline trend analysis
│   ├── disruption.go  Chaos engineering commands
│   ├── dashboard.go Full-screen dashboard
│   ├── schedule.go  Cron schedule management
│   └── ...          (health, status, version, helpers, etc.)
├── client/          HTTP API client with retry logic
│   ├── client.go    All API methods
│   └── types.go     Request/response types
├── output/          Terminal rendering utilities
│   └── output.go    Tables, banners, sparklines, config lists
├── examples/        Example scenario YAML files
└── build.sh         Cross-platform build (macOS + Linux, amd64/arm64)
```

## Development

### Build

```bash
go build -o kates .
```

### Run Tests

```bash
go test ./... -v
```

### Cross-Platform Build

```bash
bash build.sh
```

Produces binaries in `dist/` for darwin/linux × amd64/arm64.

### Lint as CI does

CI (`.github/workflows/lint.yml`) runs golangci-lint at the version pinned in
`versions.env` (`GOLANGCI_LINT_VERSION`, v2.14.0) and reports only the issues
new since the PR's base: the backlog in `.golangci.yml` is paid down file by
file, not gated at once. golangci-lint refuses a module whose Go version is
newer than the Go it was built with, and this module's `go.mod` names
`toolchain go1.27.3`, so a binary from Homebrew or a release built with an
older Go stops with "the Go language version used to build golangci-lint is
lower than the targeted Go version". Build it with the module's toolchain:

```bash
GOTOOLCHAIN=go1.27.3 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
cd cli && golangci-lint run --new-from-merge-base=main ./...
```

`make check` at the repository root runs the same command when golangci-lint
is on PATH. Drop `--new-from-merge-base` to see the whole backlog, or
`--enable-only errcheck ./cmd/...` for one linter's. A binary built with an
older Go (1.26) runs only if the `toolchain` line is removed from `go.mod` for
the run: `sed -i '/^toolchain /d' go.mod`, lint, then `git checkout go.mod`.

## Global Flags

| Flag | Description |
|------|-------------|
| `-o, --output` | Output format: `table` (default) or `json` |
| `--url` | Override API URL for a single call |
| `--context` | Use a named context for a single call |

## Configuration

Contexts are stored in `~/.kates.yaml`, similar to `kubectl` contexts:

```yaml
contexts:
  local:
    url: http://localhost:30083
  staging:
    url: https://kates.staging.example.com
current-context: local
```
