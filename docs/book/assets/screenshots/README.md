# Screenshot Conventions

Every screenshot in the book lives in this directory and follows these rules, so captures stay consistent across chapters and refreshable across releases. The book currently ships without screenshots; when adding the first ones, start with the highest-value surfaces: the interactive Lab TUI (`kates lab`), the Grafana dashboards and latency heatmaps, and Kafka UI.

## Naming

`<chapter-slug>--<subject>.png` — for example `10b-lab--test-launcher.png`, `09-observability--benchmark-dashboard.png`. The chapter slug is the chapter's filename without extension, so a rename or refresh is traceable to the chapter that owns the image. One chapter owns each image; other chapters link to that chapter rather than re-embedding.

## Capture standards

- Browser surfaces (Grafana, Kafka UI, Headlamp): 1600×1000 viewport, 2× device pixel ratio, light theme, default zoom. Crop to the meaningful panel — never a full desktop.
- Terminal surfaces (`kates lab`, `kates top`, `kates dashboard`): 120×36 terminal, the repo's default theme, font ≥ 14 pt. Prefer a real run over a mocked one.
- No personal data: capture against the local `panda` Kind cluster with the `krafter` demo cluster, never against a production environment. Redact hostnames or tokens if a real environment is unavoidable.
- Reproducibility: note the command(s) that produced the state in the embedding chapter (e.g. the test that filled the dashboard), so the capture can be regenerated after a UI change.

## Embedding

- Markdown: `![<alt text>](assets/screenshots/<name>.png)` with a caption sentence below the image.
- Alt text is mandatory and descriptive — it is what the PDF's accessibility layer and screen readers use. "Grafana benchmark dashboard showing P99 latency spiking during broker kill" beats "dashboard screenshot".
- PNG only (diagrams stay mermaid; vector art stays SVG in `assets/`).

## Refresh policy

Screenshots are release artifacts: when a captured surface changes materially (new panels, renamed commands, redesigned TUI), refresh the capture in the same PR. A screenshot more than one minor release stale should be treated as drift and refreshed or removed.
