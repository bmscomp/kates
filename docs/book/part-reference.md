# Part VI — Reference

The chapters in this Part answer "what is the exact command, endpoint or call?" rather than "how does it work?". They're for looking things up, not for reading in order, so this page gives no reading times. They add nothing to the payments question that Parts I to V follow: they're where you look up the fields, flags and exit codes its files and commands use.

You reach the Kates API through four interfaces. The CLI does most of its work through the REST API; a few commands, such as `kates deploy` and `kates ports`, run Helm or `kubectl` on your machine instead. The gRPC API covers fewer operations than REST, with typed messages that `kates.proto` defines. And `kates mcp` serves read-only tools to an AI agent over the Model Context Protocol, which the [MCP Server for AI Agents](10-cli-reference.md#mcp-server-for-ai-agents) section of the CLI Reference describes.

## What You'll Have at the End

You'll know which reference to open for a question, and that one API key serves every interface: the CLI keeps it in its context, and REST and gRPC calls send it in a header. [Authentication](11-api-reference.md#authentication), in the REST API Reference, shows how to read the key from the cluster.

## The Chapters

Each chapter documents one interface. Beyond them, `kates docs` prints man-style pages for the CLI's commands, and the Kates API serves its full OpenAPI specification at `/q/openapi`:

- [CLI Reference](10-cli-reference.md): which command and flag do you need, and how do the commands chain into a workflow?
- [REST API Reference](11-api-reference.md): which endpoint does what, how do you authenticate, and what does an error mean?
- [gRPC API Reference](16-grpc-api.md): which RPCs does the gRPC API offer, and where does it differ from REST?

## Practice

[Tutorial 14: Using Kates from an AI Agent](../tutorials/14-using-kates-from-an-ai-agent.md) connects an AI agent to the lab through `kates mcp`, and shows what each of its answers rests on.

## About the Appendices

The appendices follow this Part, and they're for looking things up too. The [Glossary](appendix-a-glossary.md) defines the terms the book uses. The [Troubleshooting Index](appendix-b-troubleshooting.md) gathers the book's troubleshooting procedures in one place. The [CI/CD Pipeline](appendix-c-cicd.md) appendix documents the GitHub Actions workflows that build, test and release Kates, which matters most if you contribute to it. The [Version & Compatibility Matrix](appendix-d-versions.md) lists every version the repository pins, generated from those pins: when a chapter and the matrix disagree, the matrix wins. [References](references.md) lists the papers, books, standards and Kafka Improvement Proposals the book cites; a number in square brackets in the text points to its entry there.
