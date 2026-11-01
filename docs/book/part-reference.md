# Part VI — Reference

You open these chapters when you already know what you want to do and need the exact command, endpoint or RPC that does it. You look things up in them instead of reading them in order, so this page gives no reading times. The payments question gets no new answer here. You come back when `kates test apply -f payments-scenarios.yaml --wait` exits 2 and you need to know what that means: the file has an error, and nothing started.

Most of what you do reaches the Kates API through the CLI, which calls the REST API. A few commands, such as `kates deploy` and `kates ports`, run Helm or `kubectl` on your machine instead. The gRPC API covers fewer operations than REST, with typed messages that `kates.proto` defines. `kates mcp` serves read-only tools to an AI agent over the Model Context Protocol; the [MCP Server for AI Agents](10-cli-reference.md#mcp-server-for-ai-agents) section of the CLI Reference describes them. [Tutorial 14: Using Kates from an AI Agent](../tutorials/14-using-kates-from-an-ai-agent.md) connects an agent to the lab, and [Tutorial 15: Authoring & Debugging YAML Suites in VS Code](../tutorials/15-ide-authoring-and-lsp.md) sets up the VS Code extension and the diagnostics of `kates lsp`. One API key serves every interface: the CLI keeps it in its context, and REST and gRPC calls send it in a header. [Authentication](11-api-reference.md#authentication), in the REST API Reference, shows how to read the key from the cluster.

Each chapter documents one interface:

- [CLI Reference](10-cli-reference.md): which command and flag you need, what it prints, and how it exits.
- [REST API Reference](11-api-reference.md): which endpoint does what, how you authenticate, and what an error means.
- [gRPC API Reference](16-grpc-api.md): which RPCs the gRPC API offers, and each place it differs from REST.

When a chapter doesn't list what you need, `kates docs` prints man-style pages for the CLI's commands, and the Kates API serves its full OpenAPI specification at `/q/openapi` to callers with the API key.

## About the Appendices

The appendices follow this Part. When a chapter uses a term you don't know, the [Glossary](appendix-a-glossary.md) defines it, and a linked term in any chapter lands on its entry. When something fails and you don't know which chapter covers it, start at the [Troubleshooting Index](appendix-b-troubleshooting.md). It lists symptoms by area, from the Kafka cluster to upgrades, and sends each one to the chapter with the fix. When a check fails on a pull request you opened against Kates, the [CI/CD Pipeline](appendix-c-cicd.md) appendix says what each GitHub Actions workflow runs.

When you need a version, read it from the [Version & Compatibility Matrix](appendix-d-versions.md). It is generated from the versions the repository pins, so when a chapter and the matrix disagree, the matrix wins. When you write a compliance reference file of your own, the [Compliance Language Reference](appendix-compliance-language.md) gives every field, what the check reads for each rule, and every finding lint reports; [Compliance Checks](compliance.md) teaches the language first. When you edit Kates YAML in an editor, the [IDE Extensions & Language Server](appendix-ide-extensions.md) appendix covers its six dialects, the offline linters, `kates lsp` and the Visual Studio Code and Visual Studio 2022 extensions. A number in square brackets in the text points to its entry in [References](references.md), the published works the book cites, Kafka Improvement Proposals among them.
