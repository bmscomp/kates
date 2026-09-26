# Book Style Sheet

One page. Every chapter follows it; `scripts/check-book-style.sh` enforces the machine-checkable rules in CI. When editing, match the file you're in; when in doubt, this sheet wins.

The rendered site is the canonical reader; GitHub's file view comes second. A chapter written before a rule existed comes into line in its own rewrite PR, not as separate churn, and the ratchet described under What CI Checks keeps it from getting worse in the meantime.

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
- Explicit ids: give a heading a `{#id}` only when it is a link target and contains punctuation or starts with a number, and always make the id the GitHub slug of the heading text: lowercase, spaces become `-`, punctuation other than `-` and `_` is dropped, leading digits stay, so " — " and " + " become `--`. For example, `### Step 1 — Deploy the Chart {#step-1--deploy-the-chart}`. The link check (lychee, which computes GitHub slugs) and Quarto (which uses the explicit id) then agree; GitHub shows the `{#…}` literally.

## Cross-References

- Link text is the target's title, verbatim: a whole chapter by its H1, `[Kafka Deployment Engineering](15-kafka-deployment.md)`; a section by its heading; a Glossary entry by the term itself. Never "Chapter N" or "Ch N" — numbers are assigned at render time and drift when the order changes.
- Appendices by title too; letters are assigned by Quarto.
- Refer to a section with a Markdown link to its heading, never "section 3.1", and don't use `@sec-` references.

## Concepts and the Glossary

- Each concept has one home chapter that explains it in full: what it is, why it matters, how it works, a worked example on `krafter`, how Kates shows it, and its limits. Everywhere else it gets a one-clause gloss and a link to that section.
- A Kates-specific term goes into the Glossary before a chapter uses it. Write each definition for the reader least likely to know the term, and end it with a link to the chapter that explains it.

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

Exactly one blank line after the closing `:::`. Genuine quotations may use plain blockquotes. Fenced divs other than callouts show as literal `:::` lines on GitHub, so don't use them.

## Code Fences and Figures

- Every fence carries a language tag: `bash`, `yaml`, `json`, `properties`, `promql`, `sql`, `protobuf`, `xml`, `mermaid`, or `text` for terminal output and ASCII UI. Never `sh`, `console`, or `shell`.
- Commands carry no `$` prompt. Captured output goes in a separate `text` block introduced by "Output:"; short inline annotations are ordinary `#` comments (no `# →` arrows).
- Explain output with a short numbered reading guide after the block, not with Quarto code annotations, which show as `# <1>` on GitHub.
- Fenced lines (Mermaid aside) stay at 90 characters or fewer: break long commands with `\` and trim long output.
- Mermaid uses plain ```` ```mermaid ```` fences (GitHub-renderable); the CI build converts them for Quarto.
- Every Mermaid block starts with `%%| label: fig-<slug>`, `%%| fig-cap:` (the figure's point, as a sentence) and `%%| fig-alt:` (what it shows, in words), which GitHub reads as Mermaid comments, and has a lead-in sentence above it.
- One idea per figure, about 8 nodes; a list belongs in a table, not a diagram. Never fake a chart or heatmap with a flowchart. Plots are generated from data committed alongside them, never drawn by hand.
- Keep diagrams narrow enough for the text column: lay long chains out `TB` and wide fan-outs `LR`, and give a subgraph with no edges crossing its border an explicit `direction` (Mermaid otherwise flips it to the opposite of its parent's). The site never shrinks labels below 70%; a wider diagram scrolls sideways and opens full screen.

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

Use the left column in prose; commands, fields and resource names in code stay as they are.

| Use | Not | Notes |
|-----|-----|-------|
| Kates | KATES | `kates` (backticked) only as command/namespace/resource |
| `krafter`, `panda` | bare names | Always backticked; gloss on first use per chapter ("the `krafter` Kafka cluster", "the `panda` Kind cluster") |
| Game Day | GameDay | The human session; `make gameday` is its automated pipeline |
| LitmusChaos | — | Full name on first use per chapter; "Litmus" after |
| Kafka UI | kafka-ui in prose | `kafka-ui` only as resource/user name |
| Kubernetes | K8s in prose | K8s acceptable only in space-constrained tables and diagram labels |
| P50 / P95 / P99 / P99.9 | p99 in prose | Lowercase only inside code, JSON fields, and PromQL |
| pre-flight | preflight | Pre-Flight in Title Case headings |
| LOAD, ROUND_TRIP, INTEGRITY | Load, Round-Trip | Enum form whenever naming a Kates test type; lowercase "round-trip" only for the generic latency concept |
| records per second (prose), rec/s (tables) | records/s, msg/s, messages/s | One unit for throughput |
| warm-up | warmup in prose | `WARMUP` stays as the scenario phase's name |
| zone | rack, AZ, availability zone | The same failure domain here: the `kafka-cluster` chart sets Strimzi's rack from `topology.kubernetes.io/zone`; `rack` only for the Strimzi field |
| `min.insync.replicas` | ISR=2 | The ISR is the set of replicas in sync, not a setting |

## Punctuation

- Spaced em dash ( — ) for asides; straight quotes only; `--` never as a prose dash (kubectl/CLI flag separators in code excepted).

## Facts

- No hardcoded counts unless the enumeration sits beside them. Versions live in the [Version & Compatibility Matrix](appendix-d-versions.md) — chapters point there instead of pinning their own.
- Documenting a command? It must exist — link or name the implementing source file in the PR description.
- Use one fixed set of names across chapters, and label every number with the environment it came from ("a LOAD run on `panda`").

## What CI Checks

`scripts/check-book-style.sh` runs in the docs workflow. It fails on an unlabeled code fence, a bold-blockquote admonition, "Chapter N" in link text, a banned term in prose, or a double blank line after `:::`. It then tests and runs the ratchet, `scripts/book_metrics.py`, against `scripts/book-metrics-baseline.json`, which holds each page's count of:

- bare headings, tables without a lead-in, bold-label bullets and hand-numbered headings;
- formula openings and changelog phrases;
- fenced lines over 90 characters, and Mermaid blocks without `fig-cap` and `fig-alt`;
- `---` rules, bold Scope blockquotes, and a broken handoff.

A page's counts may fall but never rise, a new page starts at zero, and `[TODO` fails anywhere. The metrics skip STYLE.md and README.md, which are about the book rather than in it. To see what a page is counted for, and to record a lower count:

```bash
# Every finding for one page, with line numbers
python3 scripts/book_metrics.py docs/book/05-test-types.md
# One row of counts per page
python3 scripts/book_metrics.py --summary
# After lowering a count, or splitting a chapter, record the new baseline
python3 scripts/book_metrics.py --update scripts/book-metrics-baseline.json
```

The docs workflow also parses every Mermaid diagram and checks relative links, fragments included.
