# Book Style Sheet

One page. Every chapter follows it; `scripts/check-book-style.sh` enforces the machine-checkable rules in CI. When editing, match the file you're in; when in doubt, this sheet wins.

## Voice

- Second person ("you"), present tense, active voice. Contractions are fine.
- Describe tool behavior in the present: "Strimzi generates the Secret", never "Strimzi will generate".
- "We" only inside quoted material. "The user" only for Kafka principals/`KafkaUser` resources.

## Headings

- H1 is the plain chapter title — **no** "Chapter N:" prefix. Numbering comes from the order in `_quarto.yml`; filenames are stable IDs and are never renumbered.
- H2/H3 in Title Case, unnumbered, no terminal punctuation except `?`. Exception: step-by-step install chapters may use decimal numbering (`## 3. Deploy the Chart`) — currently only the Installing Kafka chapter and the CI/CD appendix.
- Troubleshooting symptom headings: Title Case with literal error strings in backticks.

## Callouts

Quarto callouts are the only admonition syntax — never bold-blockquotes (`> **Note:**`):

- `callout-note` context worth knowing · `callout-tip` shortcut or best practice · `callout-important` must-read constraint · `callout-warning` risk of breakage · `callout-caution` risk of data loss or downtime

Exactly one blank line after the closing `:::`. Genuine quotations may use plain blockquotes.

## Code fences

- Every fence carries a language tag: `bash`, `yaml`, `json`, `properties`, `promql`, `sql`, `protobuf`, `xml`, `mermaid`, or `text` for terminal output and ASCII UI. Never `sh`, `console`, or `shell`.
- Commands carry no `$` prompt. Captured output goes in a separate `text` block introduced by "Output:"; short inline annotations are ordinary `#` comments (no `# →` arrows).
- Mermaid uses plain ```` ```mermaid ```` fences (GitHub-renderable); the CI build converts them for Quarto.

## Cross-references

- Link text is the target chapter's H1 title, verbatim: `[Kafka Deployment Engineering](15-kafka-deployment.md)`. Never "Chapter N" or "Ch N" — numbers are assigned at render time and drift when the order changes.
- Appendices by title too; letters are assigned by Quarto.

## Terminology

| Use | Not | Notes |
|-----|-----|-------|
| Kates | KATES | `kates` (backticked) only as command/namespace/resource |
| `krafter`, `panda` | bare names | Always backticked; gloss on first use per chapter ("the `krafter` Kafka cluster", "the `panda` Kind cluster") |
| Game Day | GameDay | The methodology and its automated pipeline (`make gameday`) |
| LitmusChaos | — | Full name on first use per chapter; "Litmus" after |
| Kafka UI | kafka-ui in prose | `kafka-ui` only as resource/user name |
| Kubernetes | K8s in prose | K8s acceptable only in space-constrained tables and diagram labels |
| P50 / P95 / P99 / P99.9 | p99 in prose | Lowercase only inside code, JSON fields, and PromQL |
| pre-flight | preflight | Pre-Flight in Title Case headings |
| LOAD, ROUND_TRIP, INTEGRITY | Load, Round-Trip | Enum form whenever naming a Kates test type; lowercase "round-trip" only for the generic latency concept |

## Figures

- Screenshots live in `assets/screenshots/`, named `<chapter-slug>--<subject>.png`, and follow [the capture conventions](assets/screenshots/README.md).
- Alt text is mandatory and descriptive — the PDF accessibility layer depends on it.
- Diagrams are mermaid, not images; screenshots are for surfaces mermaid can't show (TUIs, dashboards, web UIs).

## Punctuation

- Spaced em dash ( — ) for asides; straight quotes only; `--` never as a prose dash (kubectl/CLI flag separators in code excepted).

## Facts

- No hardcoded counts unless the enumeration sits beside them. Versions live in the [Version & Compatibility Matrix](appendix-d-versions.md) — chapters point there instead of pinning their own.
- Documenting a command? It must exist — link or name the implementing source file in the PR description.

## CI Enforcement

Every PR touching `docs/**` must pass the `Docs` workflow before merge (keep both jobs in the required status checks for `main`):

- `check-book-style.sh` — the machine-checkable rules on this page (fence tags, callouts, cross-reference style, terminology, version literals in prose).
- `gen-version-matrix.sh --check` — the appendix matrix matches the repo's pinned versions.
- `gen-chart-table.sh --check` — the README chart table matches `charts/*/Chart.yaml`.
- lychee (offline) — every relative link and `#fragment` in `README.md` and `docs/**` resolves.
- `check-reference-drift.sh` — the CLI, REST, and gRPC reference chapters match the implementing sources.

The `Book` workflow renders the HTML site on every book PR; HTML+PDF publish happens on push to `main`. A weekly `Link Rot` workflow validates external URLs off the PR path — its failures mean rot to triage, never a blocked merge.
