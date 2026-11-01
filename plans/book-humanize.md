# Humanizing the Kates Book: Plan

The book is correct and consistent, but in places it reads as if no one in particular wrote it. This plan makes it read like a book by an engineer who has run these tests. It follows the `humanize-writing` skill, based on Wikipedia's "Signs of AI writing" catalog. Its main point is that machine-sounding text swaps specific facts for general, important-sounding statements, so the fix is to put the specific facts back, not only to delete tells.

## Where the Book Stands

`scripts/book_voice.py` counts the catalog's patterns in prose, page by page, and lists every hit with its line for a page given as an argument. On 2026-11-01, on this branch, it found:

| Pattern | Hits | What it means here |
|---|---|---|
| Machine vocabulary (crucial, robust, leverage, serves as…) | 2 | STYLE.md already bans most of it; nothing to do at scale |
| Reframing contrasts (not just X but Y, rather than X) | 127 | The main surface tell; worst in Deploying the Strimzi Operator (13), CI/CD Pipeline (11), CLI Reference (10) |
| Filler and didactic phrases (note that, simply, just) | 54 | Small; fix in passing |
| Vague attribution and hedges (various, typically, best practice) | 45 | Small; each wants the specific fact |
| Three-item lists | 663 | Over-counted (many are real lists of three), but the rhythm is regular enough to notice |
| Sentences over 30 words | 5 to 20% per page | Highest in CI/CD Pipeline (20%), Part IV page (20%), REST API Reference (14%), Cross-Cluster Replication (14%) |

The counts confirm the September voice work held. What the counts can't measure is the larger problem the reviews named: chapters explain in general terms where a person who had done it would show one specific case. The book has three "Try it" exercises in 145,000 words, no "In practice" story at all, and six Part pages built from one template.

## The Rules

The skill's catalog applies except where `docs/book/STYLE.md` decides otherwise, and STYLE.md wins on two points:

- **Em dashes stay.** The book uses spaced em dashes for asides. Keep them, but no more than one per paragraph, and never as a substitute for a sentence break.
- **Headings stay in Title Case.** That is the book's house style, and the CI fragment checks depend on stable headings.

Everything else in the catalog applies. Three rules matter most here:

- **A reframing contrast only when the contrast is the point**, at most once per 1,000 words. "Kates measures what the client sees, not what the broker reports" earns its place, because readers confuse the two. "It's not just a test, it's a gate" does not: write "A failed gate exits 1, so the CI job fails."
- **Specific beats general.** "Clusters can behave unexpectedly under load" becomes the number from a captured run, or goes. Never invent the number: if no capture exists, cut the sentence or leave the gap in the plan's open list.
- **Keep the facts byte-identical.** A voice edit never changes a command, a flag, a value, a heading or an anchor.

## The Work

The work is done in four passes, each its own commit on the book's open pull request, so a pass reads as one diff and the book merges once.

### Pass 1 — The Pages That Set the Voice

The Preface and the six Part pages are what every reader sees first, and the earlier review of them never completed. Rewrite them by hand, not by script:

- The Preface opens with the payments question as a situation the reader is in, not with a description of the book.
- Each Part page drops the shared skeleton ("What You'll Have at the End", "The Chapters", "The Payments Question", "Practice") where a section has nothing specific to say. A Part page may be two paragraphs and a chapter list.
- Remove the reading-time line from each chapter bullet unless the author wants it kept; it repeats the same sentence shape six times.

### Pass 2 — Specifics Back In

This pass needs the lab. For each Guide chapter in Parts I to III, run its central example on `panda` and capture the real output. Then rewrite the passage around it:

- Replace a general claim with the measured one ("P99 rose from 18 ms to 240 ms while the leader moved").
- Add one or two "Try it" exercises per Concept and Guide chapter, each with the question it answers and the output the reader should see.
- Write "In practice" stories only from what actually happened on this project: an incident, a misleading number, a decision that changed. They come from the author, never from a draft; the plan lists where one would help, and the author decides.

### Pass 3 — Surface Sweep, Chapter by Chapter

Run `python3 scripts/book_voice.py docs/book/<page>.md` and reread every hit:

- Rewrite reframing contrasts to state the point directly, keeping only the ones the reader needs.
- Break long sentences where they carry two ideas, and vary sentence length so a paragraph doesn't march at one pace.
- Break a run of three-item lists where the third item is filler, and turn a list into a sentence where the items aren't parallel.
- Replace "various", "typically" and "best practice" with what is actually true, or cut the sentence.

Order by the counts: Deploying the Strimzi Operator, CI/CD Pipeline, CLI Reference, Security & Compliance, Installing Kafka, Observability & Monitoring, The Cluster Under Test, then the rest. The reference chapters (CLI, REST, gRPC) get only the surface sweep; reference prose is meant to be plain.

### Pass 4 — A Reader's Read

One person reads each chapter straight through, aloud if possible, marking every sentence that sounds like no one in particular said it. This is the only check that catches a flat paragraph with no tell in it. The author reads Pass 1 and the "In practice" stories in full.

## How Each Pass Is Checked

Every pass must keep all the book's checks green: `scripts/check-book-style.sh` with the ratchet, lychee with fragments, the Mermaid check, the Index page check and the PDF log check. On top of that:

- `scripts/book_voice.py`'s per-page counts fall for every page the pass touches.
- A diff review confirms no command, value, heading or anchor changed.
- Pass 2's captures are committed beside the chapter, or named in the pull request, so every new number has a source.

## Decisions for the Author

Decided on 2026-11-01: the book keeps "you" throughout, as STYLE.md says, and an "In practice" story is told in the second person too, with no first-person voice.

These are yours to make before Pass 1 starts:

- Which real incidents from the project may be told, and how much detail is public.
- Whether to keep the reading times on the Part pages.

## Tooling

Three installed skills load with the Skill tool, each for its own pass:

- **`humanize-writing`** runs in Pass 3, one section at a time. This plan's rules override its em-dash and heading rules.
- **`doc-coauthoring`** suits Pass 1, where the author drafts the Preface and Part pages with an assistant.
- **`engineering:documentation`** covers the chapters a reader uses rather than reads, where plain structure matters more than voice. It checks each against its document type:

| Chapter | Its type | What the skill checks |
|---|---|---|
| CLI Reference, REST API Reference, gRPC API Reference | API documentation | Every endpoint or command has a request and a response example; authentication, error codes, rate limits and pagination each have one home |
| Upgrade Playbook, Recipes & Patterns, Installing Kafka, Deploying the Strimzi Operator, Troubleshooting Index | Runbook | When to use it, prerequisites and access, numbered steps, verify, roll back, and where to escalate |
| Architecture & Design, Kafka Deployment Engineering | Architecture doc | Context and goals, the design with one diagram, the decisions and their trade-offs, the data flow |
| Introduction's Quick Start | Onboarding | First success in under five minutes on a lab that `make all` built |

  Its principles back this plan's: show, don't tell (Pass 2), lead with the most useful fact, and link rather than repeat (the Concept Registry already enforces that). It runs as its own pass on those chapters, before Pass 3, so a structural change and a voice change never share a diff.

`scripts/book_voice.py` stays a prompt for rereading, not a CI gate: a count can't tell a needed contrast from a reflex.
