# Book Enhancement Plan

Audit date: 2026-07-21. Scope: the Quarto book under `docs/book/` (~30 chapters + 4 appendices), its tooling (`scripts/check-book-style.sh`, `scripts/prepare-book-render.sh`, `scripts/gen-version-matrix.sh`), and the CI that builds and gates it (`.github/workflows/book.yml`, `.github/workflows/docs.yml`). The loose `docs/*.md` files outside the book are in scope only for the consolidation decision (P2-1).

The book is mature. Content is complete — zero `TODO`/`TBD`/`FIXME`/placeholder markers across every chapter, comprehensive prose, and strong diagram coverage in the concept chapters (`21-kafka-connect` 14 mermaid, `05-test-types` 10, `07-chaos-practice` 9, `06-chaos-theory` 8, `02-architecture` 8). The tooling is unusually good for a docs tree: CI already enforces the style sheet, version-matrix drift, chart-table sync, and offline link integrity, and renders both HTML and a diagram-rich PDF. The real gaps are narrow and specific: **a rule-vs-checker mismatch that lets style violations through**, **zero figures/screenshots for a heavily visual product**, **reference chapters that can silently drift from the code they document**, and **redundant loose docs that overlap the book**.

## Current state

| Area | State | Notes |
|---|---|---|
| Content completeness | ✓ | 0 placeholder markers; all parts populated |
| Structure / reading order | ✓ | `_quarto.yml` single source; slug-stable filenames |
| Style checker (`check-book-style.sh`) | **partial** | greps only `Note/Tip/Warning/Important/Caution` bold-blockquotes → misses `**Scope**`/`**Hypothesis**` (P0) |
| Style compliance | **7 violations** | bold-blockquotes that STYLE.md forbids but CI passes (P0-1) |
| Version drift | ✓ | `gen-version-matrix.sh --check` gates `appendix-d-versions` against repo pins |
| Link integrity | **partial** | lychee runs `--offline` only → intra-book links checked, external URLs never validated (P1-2) |
| Figures / screenshots | **0 images** | `assets/` holds only `cover.svg` + `kates-wordmark.svg`; no dashboard/TUI/UI captures (P3-1) |
| Reference accuracy | **drift risk** | CLI/REST/gRPC references hand-maintained, no check against source of truth (P1-4) |
| Chapter balance | **skewed** | `10-cli-reference` 1987 lines, `20-installation-guide` 1620 — ~2.4× the next tier (P2-2) |
| PDF build | **fragile** | best-effort; a wedged build silently 404s the download link (P1-3) |
| Loose `docs/*.md` | **6 files** | 2 orphaned stubs, 4 linked; overlap book topics (P2-1) |

---

> **Status 2026-07-21:** Weeks 1–3 executed on this branch.
> **Week 1** — P0-1 ✓ (7 bold-blockquotes → callouts) · P0-2 ✓ (checker flags any `> **` opener) · P1-1 ✓ (CI gate documented in STYLE.md; docs.yml triggers on checker/matrix scripts — marking the `Docs` jobs as required checks on `main` is a repo-settings step) · P1-2 ✓ (weekly `link-rot.yml` external lychee, off the PR path).
> **Week 2** — P1-3 ✓ (book.yml: chromium cache, last-good-PDF cache fallback with loud warnings) · P1-4 ✓ (`check-reference-drift.sh` verifies CLI/REST/gRPC chapters against cli/cmd, JAX-RS `@Path`, and kates.proto in CI; caught 6 undocumented CLI commands, a wrong `kates baseline` invocation — real form `kates test baseline` / `kates report regression` — a wrong doctor alias, and 13 undocumented REST resources; all fixed) · P2-1 ✓ (orphaned stubs deleted; `kafka-connect.md`, `kates-chaos-chart.md`, `local-development.md`, `monitoring.md` kept intentionally — each is either a redirect stub or standalone content with a pointer into the book).
> **Week 3** — P2-2 ✓ (CLI reference split into [CLI Reference] + `cli-operations.md` + `cli-security-analysis.md`; install guide split into the walkthrough + `kafka-cluster-chart-reference.md`; sections renumbered, `_quarto.yml`/README/index updated, all links+fragments verified) · P2-3 ✓ (style rule 5 flags bare version literals in prose; 17 existing hits fixed — matrix-managed pins now point to the matrix, literal defaults/historical versions backticked) · P3-1 ✓ conventions (`assets/screenshots/README.md` + STYLE.md Figures rules; actual captures need a running environment — capture recipes documented) · P3-2 ✓ verified (17-security 6 diagrams, 18-upgrade-playbook 1, 19-multi-tenancy 3 — no gaps).
> **Remaining:** P3-1 captures (needs live cluster), P3-3 reading-path worked examples, P4 items — ongoing.

## P0 — Style correctness (do first, ~half day)

**P0-1. Convert the 7 forbidden bold-blockquotes to callouts.** STYLE.md mandates Quarto callouts and forbids `> **…**` admonitions, but seven survive because the checker's grep is narrow (see P0-2). They are: `06-chaos-theory.md:161` (`> **Hypothesis:**`), and six `> **Scope**:` lines at `12-deployment.md:3`, `15-kafka-deployment.md:3`, `20-installation-guide.md:3`, `21-kafka-connect.md:5`, `deploying-strimzi-operator.md:5`, `operating-kafka-connect.md:5`. Action: convert each to `::: {.callout-note appearance="simple"}` — the exact pattern `index.qmd` already uses for its "You are reading…" note — preserving the inline cross-reference links. The `**Hypothesis:**` block reads naturally as a `callout-tip` worked example.

**P0-2. Close the rule-vs-checker gap.** `scripts/check-book-style.sh` rule 2 greps `^> \*\*\(Note\|Tip\|Warning\|Important\|Caution\):\*\*` — so any bold-blockquote using a different lead word (`Scope`, `Hypothesis`, `Goal`, …) passes CI while still violating STYLE.md's "callouts only" rule. Action: broaden the pattern to flag any `^\s*> \*\*` opener, with a short allow-list only if a legitimate use exists (there is none today). This makes the enforced check match the documented rule, so P0-1 can't silently regress.

## P1 — Enforcement & build reliability (biggest systemic gap, ~2 days)

**P1-1. Verify the full gate runs on every book PR.** `docs.yml` already runs `gen-chart-table.sh --check`, `gen-version-matrix.sh --check`, `check-book-style.sh`, and the offline lychee link-check on `docs/**`; `book.yml` renders HTML on PRs and HTML+PDF on push. Confirm these are *required* status checks on `main` (not merely present), so a style or link regression blocks merge rather than landing green. Document the gate in `STYLE.md` so contributors know what must pass.

**P1-2. Add external link validation (off the PR path).** lychee runs `--offline`, so a dead `https://` URL in any chapter is never caught. Action: add a scheduled (weekly `cron`) lychee job without `--offline`, scoped to `docs/**`, with retries and a curated ignore-list for rate-limited hosts. Keep it off pull requests to avoid flakiness blocking merges — it reports rot, it doesn't gate.

**P1-3. Harden the PDF build.** The PDF is best-effort: 102 chromium-rendered diagrams + xelatex, and on failure the site's download link 404s until the next green push (documented in `book.yml`). Action: cache the `quarto install chromium` and TinyTeX layers, add a step that fails loudly (or posts a build-summary annotation) when the PDF is absent, and keep the last-good PDF as a fallback artifact so the link never dead-ends.

**P1-4. Guard the reference chapters against code drift.** `10-cli-reference` (1987 lines), `11-api-reference`, and `16-grpc-api` are hand-maintained and can fall out of sync with the actual CLI, OpenAPI schema, and `.proto` files with nothing to catch it — the version *matrix* is checked, but not the documented surface. Action (pick per chapter): generate the command/endpoint inventory from source and `--check` it in CI (mirroring `gen-version-matrix.sh`), or add a lighter CI test that diffs documented command/endpoint/RPC names against `kates --help`, the OpenAPI doc, and the proto service. Start with the CLI reference — it is the largest and the fastest to drift.

## P2 — Consolidation & structure (~2 days)

**P2-1. Resolve the six loose `docs/*.md`.** Outside the book sit `kafka-connect.md` (86 lines, referenced by ~70 files), `kates-chaos-chart.md` (393, ref 2), `local-development.md` (93, ref 10), `monitoring.md` (24, ref 5), and two orphaned stubs `kafka-performance-testing.md` (12 lines, ref 0) and `kafka-testing-guide.md` (10 lines, ref 0). Action: delete the two orphaned stubs (their topics are fully owned by `04-performance-theory`/`05-test-types` and the tutorials). For the four linked files, decide per file — fold into the owning chapter and leave a one-line redirect stub, or keep as an intentional pointer — and record the decision. Note the ~70 inbound references to `kafka-connect.md`: audit those before moving it, or repoint them to `21-kafka-connect.md` in the same change.

**P2-2. Rebalance the two oversized chapters.** `10-cli-reference` (1987 lines) and `20-installation-guide` (1620) are ~2.4× the next tier (`12-deployment` 841, `21-kafka-connect` 839, `09-observability` 745, `17-security` 694). Action: split each along a natural seam — the CLI reference by command group, the install guide by phase (prerequisites → operator → cluster → verification) — into new **slug-named** files (never renumber; `_quarto.yml` treats filenames as stable identifiers). If the CLI reference becomes generated (P1-4), splitting may fall out for free.

**P2-3. Extend version/terminology hygiene into prose.** `gen-version-matrix.sh` gates the appendix, but individual chapters can still hardcode a version string in prose that the matrix never sees. Action: add a CI grep that flags bare `x.y.z` version literals in `docs/book/*.md` (excluding fenced code and the appendix), directing authors to reference `appendix-d-versions` instead — the rule STYLE.md already states, now enforced.

## P3 — Visual & pedagogical depth (~2–3 days)

**P3-1. Add figures for a visual product.** The book documents an interactive TUI Lab, Grafana dashboards, latency heatmaps, `kafka-ui`, and LitmusChaos ChaosCenter — and contains zero screenshots. Action: establish `assets/screenshots/` conventions (consistent capture size, dark/light, mandatory alt text for PDF/a11y), then add captures to the highest-value chapters first: `10b-lab` (the Lab TUI), `09-observability` (dashboards + heatmaps), `07-chaos-practice` (ChaosCenter), and the `kafka-ui` walkthrough. Prefer reproducible captures (documented seed/fixture) so they can be refreshed on release.

**P3-2. Diagram the under-illustrated concept chapters.** Diagram density is uneven: the reference and glossary chapters legitimately have none, but verify the operations chapters (`17-security`, `18-upgrade-playbook`, `19-multi-tenancy`) carry at least one architecture or sequence diagram each — these are exactly the chapters where a picture prevents misconfiguration. Add mermaid where a diagram would replace a dense paragraph.

**P3-3. Strengthen the end-to-end narrative.** Ensure the "reading path" recipes in `README.md` (performance / deploy / chaos / security / reference) each terminate in a runnable, cross-linked worked example that ties the tutorials (`docs/tutorials/`) back to the reference chapters, so a reader can go from theory to a green run without leaving the guide.

## P4 — DX & polish (opportunistic)

- Add Vale prose linting (terminology, passive voice, second-person consistency) to complement the grep-based `check-book-style.sh`.
- Auto-link first mentions of glossary terms to `appendix-a-glossary`.
- Add lightweight per-part landing pages (the index-page pattern) summarizing each Part's chapters.
- Add a "last reviewed" date to chapter front-matter to surface staleness, and consider EPUB output alongside HTML/PDF.

## Suggested sequence

Week 1: P0 (both) + P1-1 + P1-2. Week 2: P1-3 + P1-4 + P2-1. Week 3: P2-2 + P2-3 + P3-1. P3-2, P3-3, and P4 ongoing.

Acceptance: `check-book-style.sh` flags any bold-blockquote (0 violations remain); the style/link/version/chart-table checks are required gates on `main`; a weekly job reports external-link rot; CLI/API/gRPC references are verified against source in CI; the two oversized chapters are split into slug-named files with no renumbering; the highest-value chapters carry at least one screenshot; and the orphaned loose docs are removed with inbound references repointed.
