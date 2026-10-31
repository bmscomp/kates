# Kates for JetBrains IDEs (IntelliJ IDEA, GoLand, WebStorm)

This directory provides project-level JSON Schema mappings (`jsonSchemas.xml`) and `kates lsp --stdio` setup for JetBrains IDEs.

## 1. Enable All 6 JSON Schemas Automatically

Copy `jsonSchemas.xml` into your workspace's `.idea/` directory:

```bash
mkdir -p .idea
cp editors/jetbrains-kates/jsonSchemas.xml .idea/jsonSchemas.xml
```

IntelliJ IDEA and GoLand immediately bind all six Draft 2020-12 schemas (`schemas/*.schema.json`) to matching scenario, phased test-request, flow, resilience, disruption, and `KafkaReference` YAML files.

## 2. Connect `kates lsp --stdio` via LSP4IJ

1. Install the open-source **LSP4IJ** plugin from the JetBrains Marketplace (**Settings -> Plugins -> Marketplace -> LSP4IJ**).
2. Open **Settings -> Languages & Frameworks -> Language Servers -> + (New Language Server)**.
3. Configure the server:
   - **Name**: `Kates YAML Language Server`
   - **Command**: `kates lsp --stdio`
   - **File name patterns**: `*.yaml;*.yml` (Language Id: `yaml`)
4. Apply the settings to enable real-time cross-field diagnostics, inlay hints, quick-fixes (`Alt+Enter` / `Option+Enter`), and live cluster completions.
