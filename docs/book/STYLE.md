# Book Style Sheet

Every chapter follows this sheet; `scripts/check-book-style.sh` enforces the machine-checkable rules in CI. When editing, match the file you're in; when in doubt, this sheet wins.

The rendered site is the canonical reader; GitHub's file view comes second. Removing an older chapter's `---` rules and reflowing its text happen in that chapter's rewrite PR, not as separate churn. Other fixes to older chapters may land on their own, and the ratchet described under What CI Checks keeps the counts from growing in the meantime.

## Chapter Types

Every chapter is one of four types, and its type sets the skeleton:

| Type | Skeleton | Leaves out |
|------|----------|------------|
| Concept | Story, model, worked example, limits, then a Summary of 3–5 takeaways | Nothing |
| Guide | Hook; each concept followed by the hands-on block that exercises it; a Summary of takeaways | Nothing |
| Task (installs, upgrades, recipes) | One short prose paragraph with the goal, prerequisites and time; then steps, verify, roll back and next | Objectives, Summary, bold labels |
| Reference, and every appendix | A purpose line, then a "How to Read an Entry" section | Objectives, Summary, Try it |

A chapter written before the types keeps its objectives and Summary until its rewrite.

## Openings and Closings

- Open with a problem, a concrete situation, or a claim the chapter defends. Never open with "This chapter covers / provides / walks you through / is for". Objectives are optional: 2–5 bullets, after the hook.
- The Summary has 3–5 bullets of 25 words or fewer, with no flags, paths or version numbers, and says what the reader now knows or can do.
- The final paragraph hands off to the next chapter in `_quarto.yml` order: it names that chapter by its H1 title, gives a reason, and its last link points there. Write it by hand, and never as "With X in place, …". CI checks the chain: part pages are skipped, the last chapter before the appendices hands off to the first appendix, and appendices need no handoff.

## Voice

- Second person ("you"), present tense, active voice. Contractions are fine.
- Describe tool behavior in the present: "Strimzi generates the Secret", never "Strimzi will generate".
- "We" only inside quoted material. "The user" only for Kafka principals/`KafkaUser` resources.
- No marketing adjectives: purpose-built, rich, deep, powerful, comprehensive, ultimate, particularly valuable.
- A Java class is never the subject of a sentence: lead with the behavior, and name the class once in parentheses if contributors need it.
- No history in chapters ("no longer", "used to", "since chart X", "until this refactor", "are gone"): say what is true now.
- Model first, limits after. Limits go in a "Limits" subsection or a single callout, one limit per sentence. Put conditional rules in a table.
- Split any explanatory sentence over 40 words, and keep a chapter's share of 40-plus-word sentences at 10% or less (`scripts/book_metrics.py` reports it).
- Write for the book's readers: platform engineers, SREs, performance engineers, security reviewers and developers. Repository internals (dashboard generators, CI workflows, annotation counts, class maps) are contributor material and stay out of the chapters.
- Tell a real incident or decision as "In practice": what went wrong, what the numbers showed, what changed. Never invent one; leave it out instead.

## Sections, Lists and Tables

- Every H2 and H3 opens with at least one sentence of prose. Every table, code block and diagram gets a lead-in that says what to look at and what to notice, and a table whose meaning isn't obvious gets a takeaway sentence after it. Entries in the three reference chapters (a command, endpoint or RPC heading over its usage block) are exempt.
- A blank line before every list. Avoid bold-label bullets (`- **Label**: text`): write prose, or use a table when the items really are label–value pairs.
- Table cells hold 25 words or fewer; anything longer becomes prose. Write status as words (Yes, No), never ✅ or ❌, which a PDF font without the glyph drops silently. Don't bold the first-column labels. Set the pipe-table dash ratios so description columns get the width.

## Headings and Anchors

- H1 is the plain chapter title — **no** "Chapter N:" prefix. Numbering comes from the order in `_quarto.yml`; filenames are stable IDs and are never renumbered.
- H2/H3 in Title Case, unnumbered, no terminal punctuation except `?`. No heading starts with a hand-typed number (the check is `^#+ \d`), in any chapter. A procedure is an ordered list, or H3s titled "Step N — Verb the Thing".
- Rename repeated headings so they say something.
- Troubleshooting symptom headings: Title Case with literal error strings in backticks.
- Explicit ids: give a heading a `{#id}` only when it is a link target and its GitHub slug differs from Quarto's automatic id, which happens with " — ", " + ", " & " or a leading number (both drop parentheses, backticks and quotes the same way). Always make the id the GitHub slug of the heading text: lowercase, spaces become `-`, punctuation other than `-` and `_` is dropped, leading digits stay, so " — " and " + " become `--`. For example, `### Step 1 — Deploy the Chart {#step-1--deploy-the-chart}`. The link check (lychee, which computes GitHub slugs) and Quarto (which uses the explicit id) then agree; GitHub shows the `{#…}` literally. The Glossary's `{#gl-…}` ids are the one exception, as [Concepts and the Glossary](#concepts-and-the-glossary) explains.

## Cross-References

- Link text is the target's title, verbatim: a whole chapter by its H1, `[Kafka Deployment Engineering](15-kafka-deployment.md)`; a section by its heading; a Glossary entry by the term itself, singular or plural. Never "Chapter N" or "Ch N" — numbers are assigned at render time and drift when the order changes.
- Appendices by title too; letters are assigned by Quarto.
- Refer to a section with a Markdown link to its heading, never "section 3.1", and don't use `@sec-` references.

## Concepts and the Glossary

Each concept has one home chapter that explains it in full: what it is, why it matters, how it works, a worked example on `krafter`, how Kates shows it, and its limits. The [Concept Registry](CONCEPTS.md) lists every concept with its home, the gloss other chapters use and its Glossary anchor.

- Outside its home, a concept gets its gloss from the registry, or a clause cut from it, and a link to the home section.
- A new concept gets a registry row and a Glossary entry in the pull request that first teaches it, and a Kates-specific term goes into the Glossary before a chapter uses it.
- The Glossary has one letter H2 per initial (`## A`) and one H3 per term, with an explicit id: `### Consumer Lag {#gl-consumer-lag}`. The id is `gl-` and a slug of the term in lowercase ASCII: backticks are dropped, and each run of spaces or punctuation between words becomes one `-`, so `min.insync.replicas` gives `gl-min-insync-replicas`. A parenthetical qualifier is either dropped or folded into the slug: the entry for Percentile (P50, P95, P99, P99.9) is `gl-percentile`, and the one for Context (CLI) is `gl-cli-context`.
- A `gl-` id is the one explicit id that isn't a GitHub slug: lychee honours it and Quarto uses it, but GitHub's file view doesn't. Set `toc-depth: 2` in the Glossary's front matter, so that its table of contents stops at the letters.
- An entry is a one-sentence definition for the reader least likely to know the term, one or two "In Kates" sentences where Kates gives the term a meaning of its own, and a link to its home.
- Link a Glossary term at its first use in each chapter's running prose, such as `[consumer lag](appendix-a-glossary.md#gl-consumer-lag)`. Running prose is paragraphs, list items, blockquotes and table body rows; headings, code, link text, callout titles and table header rows don't count. An inline code span counts only when it is the whole term, and a sentence that defines the term isn't linked.

## The Running Example

Where a chapter needs a worked example, use the book's running example: is `krafter` ready for a payments workload? Write it in the second person ("you run the payments platform"), with no invented people, companies or incidents, and never state an outcome you didn't capture. [The Running Example](CONCEPTS.md#the-running-example) in the Concept Registry gives its targets, names, files and the artifact each Part adds; use its names and values exactly.

## Callouts

Quarto callouts are the only admonition syntax — never bold-blockquotes (`> **Note:**`). The five types, with their colors in `theme/kates-light.scss` and `theme/kates-dark.scss`:

- `callout-note` (blue): context worth knowing
- `callout-tip` (green): a shortcut or best practice
- `callout-important` (indigo): a must-read constraint
- `callout-warning` (amber): risk of breakage
- `callout-caution` (red, the only red): risk of data loss or downtime

Every callout has a descriptive title (`::: {.callout-note title="…"}`) and uses the full `::: {.callout-*}` syntax. Beyond that:

- At most one callout per H2 in guide and task chapters, never two back to back, three sentences or fewer, and never a procedure inside one.
- An exercise is `::: {.callout-tip title="Try it: <the question it answers>"}`, not a bold first line. It follows the concept it exercises and never repeats the Quick Start.
- A product bug appears only as `::: {.callout-warning title="Known limitation: <what>"}`, with the issue link and the workaround; delete it when the fix ships. Never describe in prose a mismatch between help text and actual behavior.
- Scope goes in the opening prose or in a `callout-note` titled "Scope", never in a bold blockquote.

Exactly one blank line after the closing `:::`. Genuine quotations may use plain blockquotes. Callouts are the only fenced divs for now; every fenced div, callouts included, shows as literal `:::` lines on GitHub.

## Code Fences and Figures

- Every fence carries a language tag: `bash`, `yaml`, `json`, `properties`, `promql`, `sql`, `protobuf`, `xml`, `mermaid`, or `text` for terminal output and ASCII UI. Never `sh`, `console`, or `shell`.
- Commands carry no `$` prompt. Captured output goes in a separate `text` block introduced by "Output:"; short inline annotations are ordinary `#` comments (no `# →` arrows).
- Explain output with a short numbered reading guide after the block, not with Quarto code annotations, which show as `# <1>` on GitHub.
- Fenced lines (Mermaid aside) stay at 90 characters or fewer: break long commands with `\` and trim long output.
- Mermaid uses plain ```` ```mermaid ```` fences (GitHub-renderable); the CI build converts them for Quarto.
- Every Mermaid block starts with `%%| label: fig-<slug>`, `%%| fig-cap:` (the figure's point, as a sentence) and `%%| fig-alt:` (what it shows, in words), which GitHub reads as Mermaid comments, and has a lead-in sentence above it.
- One idea per figure, about 8 nodes; a list belongs in a table, not a diagram. Never fake a chart or heatmap with a flowchart. Plots are generated from data committed alongside them, never drawn by hand.
- Keep diagrams narrow enough for the text column: lay long chains out `TB` and wide fan-outs `LR`, and give a subgraph with no edges crossing its border an explicit `direction` (Mermaid otherwise flips it to the opposite of its parent's). On the site a diagram that fits shows at full size, from 1200px up reaching into the right margin; one a little too wide shrinks to the column; a wider one shows whole as a thumbnail whose Expand button opens it full screen. The PDF shrinks every diagram to the page, so one drawn too wide prints too small to read.

## Page Furniture

- No `---` horizontal rules.
- One paragraph per line, with no hard wrapping. US spelling.
- Aim for 3,000–6,000 words per chapter, and split one that passes 8,000.
- A Part's landing page is a top-level `part-<slug>.md` with no front matter.
- `[TODO: …]` is for drafts only. CI fails on `[TODO` in any page of the book, so a rewrite merges only when its facts are in hand; otherwise keep the old text, or leave the section out.

## Accessibility

- Every image and plot has alt text, and no meaning is carried by color alone.
- A palette change keeps every text color at WCAG AA contrast (4.5:1) against its surface, as the comments in `theme/kates-light.scss` and `theme/kates-dark.scss` require.
- Every new component ships with dark-mode colors and a visible focus style.

## Terminology

Use the left column in prose; commands, fields, flags, UI labels and resource names in code stay as they are. The Meaning column is what the word means in this book, checked against the code:

| Use | Not | Meaning |
|------------------|----------------------|--------------------------------------------|
| Kates | KATES | The product. `kates` (backticked) only as the command, the namespace or a resource name |
| the Kates API | Kates backend, Kates engine, backend engine, Backend Engine, Benchmark Engine, Kates application | The Quarkus server behind the REST and gRPC APIs, which runs tests and faults. For the workload, say "the Kates API pod" or "Deployment" |
| the Kates API, the benchmark backend or the chaos provider, whichever is meant | backend, the backend (unqualified) | Unqualified, "backend" can mean any of the three. `backend` stays as the test field and `--backend` as the flag |
| benchmark backend: the `native` backend, the Trogdor backend | workload engine, workload backend, backend engine, Benchmark Engine | What generates load, set by a test's `backend` field: `native` (the default) runs clients inside the Kates API; `trogdor` submits workloads to a Trogdor coordinator |
| Trogdor (as a benchmark backend only) | Trogdor fault injection, Trogdor chaos | Kates submits only produce, consume and round-trip workload specs to Trogdor; it never injects faults through it |
| chaos provider: the `litmus-crd`, `kubernetes`, `hybrid` or `noop` provider | chaos backend, chaos engine, the Litmus backend, the Kubernetes backend | What carries out a fault, set by `kates.chaos.provider`: `litmus-crd` (the default), `kubernetes`, `hybrid` (picks one at startup) or `noop` (injects nothing) |
| gate | quality gate, validation gate, SLA check | A threshold in a scenario's `validate` block, which `kates test apply --wait` checks. "SLA gate" means the same. `kates gate` gives a performance grade |
| SLA grade, or grade | SLA verdict, SLA score, letter grade (as a name) | The letter a disruption plan's `sla` block earns: A, B, C, D or F, or `-` when nothing could be evaluated. A plan without `sla` gets none |
| verdict | integrity status, integrity result (for the outcome), SLA verdict | An INTEGRITY run's outcome: PASS, DATA_LOSS, CORRUPTION, ORDERING_VIOLATION or DUPLICATES_DETECTED, read with `kates test get`. The "SLA Verdict" that `kates report show` prints is a UI label, not a verdict |
| SLA | SLO (for Kates's thresholds), SLA contract | Kates's word for targets in a scenario's `validate` block, a plan's `sla` block (and `--fail-on-sla-breach`) and the `sla` of a phased `scenario` sent to `POST /api/tests`. They're SLO-style targets, not agreements; say so at first use |
| performance grade | grade (unqualified), quality gate, score | The letter `kates gate` gives the test run it starts, from its average throughput and P99 against fixed thresholds; `kates benchmark` grades from a score |
| security grade | grade (unqualified), security score | The letter `kates security audit` gives the cluster from its security checks; `kates security gate --min-grade` fails below it |
| fault | failure injection, chaos (as a count noun), attack | One injected failure: a disruption type aimed at the pods a selector picks, held for `chaosDurationSec` (a `faultSpec` or `chaosSpec`) |
| disruption type | fault type, chaos type, experiment type | Which fault a step injects: a `DisruptionType` value, such as `POD_KILL` or `NETWORK_PARTITION`, from the list `kates disruption types` prints |
| disruption plan, or plan | chaos plan, experiment plan, disruption scenario | A named, ordered list of fault steps with guardrails and an optional `sla` block, run by `kates disruption run --config` or `POST /api/disruptions` |
| playbook | built-in scenario, chaos scenario | A disruption plan built into Kates, run by name with `kates disruption playbook run`; it can't carry an `sla` block |
| disruption | disruption test, chaos run, chaos test | One run of a disruption plan or playbook: its ID, status, report, timeline and, when the plan has `sla`, its SLA grade |
| resilience run | chaos test, combined test; "resilience test" only when quoting the CLI's help or the REST API's section | One Kates test with one fault injected after `steadyStateSec`, run by `kates resilience run`. It reports before-and-after impact, with no safety guard, rollback or grade |
| chaos experiment | a name for a Kates object | Chaos engineering's unit: faults injected to test a steady-state hypothesis. In Kates you run one as a disruption or a resilience run |
| Game Day | GameDay, game day | The human session: a team, a hypothesis, a rollback plan, a debrief. `make gameday` runs `scripts/gameday.sh`, its automated pipeline |
| LitmusChaos | — | Full name on first use per chapter; "Litmus" after |
| Kafka UI | kafka-ui in prose | `kafka-ui` only as a resource or user name |
| Kubernetes | K8s in prose | K8s only in space-constrained tables and diagram labels |
| P50 / P95 / P99 / P99.9 | p99 in prose | Lowercase only inside code, JSON fields and PromQL |
| pre-flight | preflight | Pre-Flight in Title Case headings |
| LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY | Load test, Round-Trip, round trip test, latency test | Enum form for Kates test types; TUNE_REPLICATION, TUNE_ACKS, TUNE_BATCHING, TUNE_COMPRESSION, TUNE_PARTITIONS and INTEGRATION_CDC complete the `TestType` enum. Lowercase "round-trip" only for the general idea |
| records per second (prose), rec/s (tables) | records/s, msg/s, messages/s | One unit for throughput |
| warm-up | warmup in prose | `WARMUP` stays as the scenario phase's name |
| `krafter` | the Kafka cluster (unglossed), kafka-cluster (a chart, and `make kafka`'s release) | The Kafka cluster under test: the `kafka-cluster` chart's `clusterName` and `kates deploy`'s `--kafka-name` default. Always backticked; gloss on first use per chapter |
| `panda` | the Kind cluster (unglossed), the local cluster | The three-node Kind cluster `make cluster` creates from `config/cluster.yaml`. Always backticked; gloss on first use per chapter |
| `alpha`, `sigma`, `gamma` | node-1, zone-a, AZ1 | The three `panda` nodes, each named after the zone its `topology.kubernetes.io/zone` label gives it |
| zone | rack, AZ, availability zone | The same failure domain here: the `kafka-cluster` chart sets Strimzi's rack from `topology.kubernetes.io/zone` by default; `rack` only for the Strimzi field |
| isolated topology, single-namespace topology | Isolated Topology (capitalized in prose), multi-namespace topology, single topology | The namespace layout `kates deploy --topology` picks: `isolated` (the default) or `single` (one namespace, `kates-stack` by default). The operator is in `strimzi-operator` either way |
| node layout | topology (for how nodes, brokers and pods are arranged) | How nodes and pods are arranged, like the Deployment Guide's Minimal, Standard and Production. "Topology" is the namespace layout; `kates cluster topology` keeps its name |
| RTO, RPO (as measured) | recovery time objective, recovery point objective (for what a report shows) | Measurements in Kates reports: RTO a recovery time, RPO how far before the fault the oldest lost record was sent. `maxRtoMs` and `maxRpoMs` are objectives |
| `min.insync.replicas` | ISR=2 | The ISR is the set of replicas in sync, not a setting |

## Punctuation

- Spaced em dash ( — ) for asides; straight quotes only; `--` never as a prose dash (kubectl/CLI flag separators in code excepted).

## Facts

- No hardcoded counts unless the enumeration sits beside them. Versions live in the [Version & Compatibility Matrix](appendix-d-versions.md) — chapters point there instead of pinning their own.
- Documenting a command? It must exist — link or name the implementing source file in the PR description.
- Use one fixed set of names across chapters, and label every number with the environment it came from ("a LOAD run on `panda`").

## Citations

- Cite a published work where the book states what it established: a paper, a book, a standard or RFC, a JDK Enhancement Proposal, or the Kafka Improvement Proposal (KIP) that defines a feature a chapter explains. Product manuals, Strimzi and Helm how-tos, and Kates's own behavior get links or source references, not citations.
- Write `[@key]` at the end of the clause the work supports, before its punctuation: `…in a replicated log [@ongaro2014search].` Several works share one bracket: `[@kip98; @wang2021consistency]`. Name a KIP in the prose when its number means something to the reader, as `KIP-896 [@kip896]`, and don't also link it inline.
- Cite at a concept's first explanation in each chapter and at its Glossary entry, not at every mention. Headings, table cells, Summaries and code carry no citations.
- A new work gets an entry in `references.bib`, each field checked against the source itself, as the file's header says. The site and the PDF render `[@key]` as a number linked to [References](references.md), which lists every entry, numbered in alphabetical order of first author. GitHub's file view shows the key.

## What CI Checks

`scripts/check-book-style.sh` runs in the docs workflow. It fails on an unlabeled code fence, a bold-blockquote admonition, "Chapter N" in link text, a banned term in prose, a double blank line after `:::`, a citation of a key `references.bib` doesn't have, or an entry there that nothing cites. It then tests and runs the ratchet, `scripts/book_metrics.py`, against `scripts/book-metrics-baseline.json`, which holds each page's count of:

- bare headings at any level, not only H2 and H3: a heading followed directly by another heading, a table or a code block (reference entries are exempt);
- tables without a lead-in, bold-label bullets and hand-numbered headings;
- formula openings and changelog phrases;
- fenced lines over 90 characters, and Mermaid blocks without `fig-cap` and `fig-alt`;
- `---` rules, bold Scope blockquotes, and a broken handoff.

A page's counts may fall but never rise, a new page starts at zero, and `[TODO` fails anywhere. The style check and the metrics skip STYLE.md, README.md and CONCEPTS.md, which are about the book rather than in it. To see what a page is counted for, and to record a lower count:

```bash
# Every finding for one page, with line numbers
python3 scripts/book_metrics.py docs/book/05-test-types.md
# One row of counts per page
python3 scripts/book_metrics.py --summary
# After lowering a count, or splitting a chapter, record the new baseline
python3 scripts/book_metrics.py --update scripts/book-metrics-baseline.json
```

The docs workflow also parses every Mermaid diagram and checks relative links, fragments included.
