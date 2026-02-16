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
