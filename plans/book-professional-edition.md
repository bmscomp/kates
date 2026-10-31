# The Kates Book, Professional Edition: Enhancement Plan

This plan takes *Kates — The Definitive Guide* from a well-kept project manual to a book a technical publisher would print. It comes from an editorial review on 2026-10-31 of `main` at 365c0f15, rendered as the site and as the 716-page PDF, and it is carried out on the same branch as this file.

## How the Review Was Done

Each of 27 chapters had one reviewer acting as development editor, copy editor and technical reviewer. The technical reviewer checked at least twelve claims per chapter against the CLI, the Kates API and the charts. The print and site reviews were done on the rendered PDF and site, with a four-chapter sample book for fast LaTeX experiments. The reviews listed under Not Covered did not complete.

The table counts the findings by severity:

| Severity | Findings | Meaning |
|---|---|---|
| Critical | 20 | Wrong, dangerous or broken in a published book |
| Major | 302 | Clearly below the professional standard |
| Minor | 440 | A reader notices |
| Polish | 142 | Only an editor notices |
| Total | 904 | |

## What a Professional Edition Needs

The review found the book's structure and voice sound. Its weakest axis is accuracy: chapters still describe behavior the code has changed, and some figures draw pipelines the engine doesn't run. Its second weakness is print: the PDF sets 134 lines past the margin and has no copyright page, colophon or index. The plan has four tracks.

### Track 1 — Print Production

These changes are proven on a scratch render of the whole book, where they take the overfull lines from 134 (the widest 115 pt) to 14:

- **Wrap long code lines.** Load `fvextra` with `breaklines` and a muted continuation arrow, so a line past the measure wraps instead of running off the page.
- **Let inline code break.** A PDF-only filter, `theme/pdf-inline-code.lua`, lets code of 18 characters or more break after `/ . - _ : =`.
- **Size table columns from their content.** A PDF-only filter, `theme/pdf-tables.lua`, gives every table relative column widths and never less than its longest unbreakable token needs.
- **No widows or orphans.** `\clubpenalty`, `\widowpenalty` and `\displaywidowpenalty` at 10000.
- **Copyright page and colophon.** A page after the title with the license, the edition, the source commit, how to report errata, and the typefaces and tools.
- **A back-of-book index.** One committed term list, `docs/book/index-terms.txt`, drives both editions: a Lua filter marks the PDF for `makeindex`, and `scripts/gen-book-index.py` writes the site's Index page, which CI keeps in sync.
- **A stricter PDF log check.** The pull-request PDF job reports overfull lines in code, tables and prose with their pages, not only overfull images.
- **Deferred:** a 7 × 10 in trim for print on demand (18% more pages in a sample) and a print cover with spine and back. Both need a printer's specification first.

### Track 2 — The Site

Three site changes come from the review:

- **Landing page claims.** The hero banner says "Chaos Mesh Resilience", but Kates injects faults through LitmusChaos and the Kubernetes API, and its copy breaks the style sheet's no-marketing rule. Rewrite it with only true claims.
- **An Index page** in the appendices, generated from the term list.
- **Figures that print a literal `\n`.** Diagrams show the escape sequence as text on the site and in the PDF. Fix them, and make the Mermaid check fail on it.

### Track 3 — Accuracy and Content

Every chapter's findings follow: the critical ones in full and the rest as a checklist. The implementer re-checks each "Fact:" finding against the code before changing the text, and drops a finding that no longer holds.

### Track 4 — Checks

A Mermaid label holding a literal `\n` fails `scripts/check-mermaid.sh`, `scripts/gen-book-index.py --check` runs in the docs workflow, and the metrics ratchet records the lower counts the edits produce.

## Findings by Chapter

Each chapter gives its reviewer's scores (1 to 5), its critical findings in full, and every other finding by severity. The checklist is the implementation's to-do list.

### `01-introduction.md`

Scores: clarity 4, accuracy 4, structure 3, voice 4, completeness 2, examples 2.

The other findings, by severity:

- [ ] Major: Dev: The chapter never states the payments question the registry says it states (`01-introduction.md:122`)
- [ ] Major: Dev: 'Safe by Default' teaches the guard's limits before the reader has met a plan (`01-introduction.md:73`)
- [ ] Major: Dev: The Quick Start's API-key paragraph and note are an operations aside, not a quick start (`01-introduction.md:149`)
- [ ] Major: Dev: The chapter promises 'read its report' and shows no report (`01-introduction.md:9`)
- [ ] Major: Dev: fig-intro-readiness is a nine-item list drawn as a 13-node tree (`01-introduction.md:27`)
- [ ] Major: Copy: Both callouts have no title and render as 'Note' and 'Tip' (`01-introduction.md:151`)
- [ ] Major: Fact: 'kates ports stops every kubectl port-forward already running' is stale (`01-introduction.md:152`)
- [ ] Minor: Dev: fig-intro-workflow names no Kates command, so it teaches a generic pipeline (`01-introduction.md:95`)
- [ ] Minor: Copy: Quick Start carries two callouts, one of five sentences, one holding a procedure (`01-introduction.md:152`)
- [ ] Minor: Copy: Five hand-numbered H3s under Design Philosophy (`01-introduction.md:55`)
- [ ] Minor: Copy: A 100-character fence line scrolls the Quick Start block at every width (`01-introduction.md:135`)
- [ ] Minor: Copy: Two sentences over 40 words (`01-introduction.md:73`)
- [ ] Minor: Copy: Summary bullets over 25 words (`01-introduction.md:189`)
- [ ] Minor: Copy: Glossary terms linked late or not at all (`01-introduction.md:14`)
- [ ] Minor: Copy: Section links carry the chapter title as link text (`01-introduction.md:73`)
- [ ] Minor: Copy: Hardcoded count '6 built-in playbooks' with no enumeration beside it (`01-introduction.md:82`)
- [ ] Minor: Fact: 'Performance Testing' row lists INTEGRITY as a performance type and hides six test types (`01-introduction.md:81`)
- [ ] Minor: Fact: 'kates report compare' prints raw JSON, so the Try it's promised comparison is a JSON dump (`01-introduction.md:172`)
- [ ] Minor: Fact: The Export Formats row omits three of the six export formats (`01-introduction.md:85`)
- [ ] Minor: Dev: The Feature Overview table is a product matrix that repeats the sections around it (`01-introduction.md:75`)
- [ ] Minor: Dev: At 1,488 words the chapter is half STYLE.md's floor, and the gaps are the missing pieces (`01-introduction.md:1`)
- [ ] Minor: Dev: Objectives promise recall and an unkept report reading (`01-introduction.md:7`)
- [ ] Minor: Dev: Both figures land as thumbnails on a phone; neither needs to be a figure there (`http://127.0.0.1:8765/01-introduction.html figure.kates-diagram.is-oversized`)
- [ ] Polish: Copy: 'Into' capitalized in a Title Case heading (`01-introduction.md:91`)
- [ ] Polish: Copy: Serial comma used in the Summary and dropped in the body (`01-introduction.md:3`)
- [ ] Polish: Copy: Bold used for emphasis in running prose (`01-introduction.md:120`)
- [ ] Polish: Copy: Passive history and 'designed for' openings (`01-introduction.md:53`)
- [ ] Polish: Copy: SLA's SLO caveat comes 47 lines after the term's first use (`01-introduction.md:18`)
- [ ] Polish: Fact: The deploy-writes-the-key rule omits one of its four conditions (`01-introduction.md:149`)
- [ ] Polish: Fact: The quoted 401 is a prefix of the real line (`01-introduction.md:149`)
- [ ] Polish: Dev: Tutorials link leaves the site while the Part page links the tutorials inside it (`01-introduction.md:157`)
- [ ] Polish: Dev: 'What Is Kates?' restates the Architecture chapter's opening sentence (`01-introduction.md:14`)

### `02-architecture.md`

Scores: clarity 3, accuracy 3, structure 2, voice 3, completeness 3, examples 2.

The other findings, by severity:

- [ ] Major: Fact: Migration History stops at V15; the repository is at V28 (`02-architecture.md:493-511`)
- [ ] Major: Fact: the Kates API pod is not limited to 512 MiB on non-Kind clusters (`02-architecture.md:63`)
- [ ] Major: Fact: Technology Stack versions are stale or empty (Go, Jaeger, nine 'Latest' cells) (`02-architecture.md:375-393`)
- [ ] Major: Fact: `kates health` never prints 'Kafka: ❌ Disconnected' (`02-architecture.md:519`)
- [ ] Major: Fact: the ER diagram shows 5 of 19 tables with columns that are behind the schema (`02-architecture.md:427-491`)
- [ ] Major: Dev: half the chapter is contributor material — Java class tours, lock tables, migrations (`02-architecture.md:71-179, 285-290, 423-511`)
- [ ] Major: Dev: section order splits each story in two; headings say little (`02-architecture.md:10-541`)
- [ ] Major: Dev: the Try it breaks the exercise format and ignores the running example (`02-architecture.md:526-539`)
- [ ] Major: Dev: Graceful Degradation table should be prose; its cells run to 40 words (`02-architecture.md:517-522`)
- [ ] Major: Dev: three list-diagrams add no idea the prose lacks (Component Map, CLI, Data Model) (`02-architecture.md:75-121, 248-283, 427-491`)
- [ ] Major: Copy: ten bold-label bullets and three bold-verb bullets (`02-architecture.md:127-131,139-142,287-290`)
- [ ] Minor: Fact: the CLI figure's report node omits `heatmap` and `brokers snapshot` (`02-architecture.md:262`)
- [ ] Minor: Fact: the histogram figure's bucket edges don't exist in the code (`02-architecture.md:148-170`)
- [ ] Minor: Dev: the LOAD sequence never shows PostgreSQL, the chapter's main thesis (`02-architecture.md:296-331`)
- [ ] Minor: Dev: Disruption Engine figure carries three ideas; the lead-in points at one (`02-architecture.md:185-234`)
- [ ] Minor: Dev: the opening is a disguised 'this chapter covers'; the real hook is on line 12 (`02-architecture.md:3,12`)
- [ ] Minor: Dev: the Summary names classes and numbers instead of what the reader can now do (`02-architecture.md:543-547`)
- [ ] Minor: Dev: the handoff gives the next chapter a job it doesn't do (`02-architecture.md:549`)
- [ ] Minor: Dev: Safety Guardrails and Disruption Types re-explain what Chaos Engineering in Practice owns (`02-architecture.md:236-242`)
- [ ] Minor: Dev: CONCEPTS.md still calls Where Results Live 'planned' and names a section that doesn't exist (`CONCEPTS.md:31; 02-architecture.md:395`)
- [ ] Minor: Dev: the reader never learns how the CLI reaches port 8080 (`02-architecture.md:45-54`)
- [ ] Minor: Dev: at 2,394 words the chapter is under the floor, and the cuts above take it lower (`02-architecture.md`)
- [ ] Minor: Copy: 'Rich terminal output' uses a banned marketing adjective (`02-architecture.md:288`)
- [ ] Minor: Copy: a product limitation described in prose instead of a Known limitation callout (`02-architecture.md:290`)
- [ ] Minor: Copy: five sentences over 40 words (`02-architecture.md:69,242,246,421,538`)
- [ ] Minor: Copy: class names as H3 titles and bold emphasis in running prose (`02-architecture.md:69,123,135,144,146,246`)
- [ ] Minor: Copy: garbled sentence about the single topology (`02-architecture.md:47`)
- [ ] Minor: Copy: Glossary terms unlinked at first use (profile, snapshot, platform profile) (`02-architecture.md:246,411,415`)
- [ ] Minor: Figures: the parts diagram is the right figure but unreadable on a phone (`http://127.0.0.1:8765/02-architecture.html #fig-architecture-parts; shots/ch-02/`)
- [ ] Polish: Dev: the one constraint worth a callout has none (`02-architecture.md:413`)
- [ ] Polish: Copy: fragment lead-ins ('Key methods:', 'Key design decisions:', 'This diagram traces…:') (`02-architecture.md:172,285,294,335`)
- [ ] Polish: Copy: British 'cancelled' in prose, and serial-comma inconsistency (`02-architecture.md:521; 3,59,142,246,288`)
- [ ] Polish: Copy: terminology drift in figure text ('Litmus provider', 'messages', 'Kafka Cluster') (`02-architecture.md:139,190,305,313`)
- [ ] Polish: Figures: the fig-caps of the CLI and histogram figures don't state a point (`02-architecture.md:150,250`)

### `03-cluster.md`

Scores: clarity 4, accuracy 3, structure 3, voice 3, completeness 4, examples 2.

**Critical — Fact: 'Testing Without Rack Awareness' cannot work on panda and strands zone-pinned pods.** At `03-cluster.md:275-288`. Lines 279-283 tell the reader to run `kubectl label node alpha topology.kubernetes.io/zone-` on all three nodes 'to test what happens when all replicas land on the same zone'. The generated pools carry `zone:` (cli/pkg/detect/valuesgen.go:489-500), which the chart renders as `nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution` on the zone key (charts/kafka-cluster/templates/nodepools.yaml:54-55); once the labels are gone, any Kafka pod that restarts has no schedulable node. And with exactly one broker per node and RF=3 (values.yaml:136), every partition already has one replica on each

Fix: Delete the H3 and its warning. If the idea is worth keeping, replace it with two sentences in 'Node Labeling and Zone Simulation': 'Rack-aware placement acts only when a partition is created, and on `panda`, with one broker per zone, every partition has one replica per zone whatever the labels say. To see an unaware layout you need more brokers than zones (see Testing with More Brokers) or a cluster whose pools set no `zone`; never remove the node labels, because the pools' `nodeAffinity` selects on them and a restarted pod would stay Pending.'

The other findings, by severity:

- [ ] Major: Fact: 'Testing with More Brokers' edits 0.4 keys that kates deploy no longer writes (`03-cluster.md:215-259`)
- [ ] Major: Fact: zone-drain recipe names pods that don't exist and leaves the reader stranded (`03-cluster.md:263-273`)
- [ ] Major: Fact: broker-failure phase durations are unsourced and the detection window contradicts Kafka's default (`03-cluster.md:187-191`)
- [ ] Major: Dev: all six callouts are untitled and the 'Try it' is a bold line CI cannot count (`03-cluster.md:103, 179, 286, 345, 379, 383`)
- [ ] Major: Dev: Summary has six bullets of up to 42 words and restates settings (`03-cluster.md:398-405`)
- [ ] Major: Dev: Access Points duplicates Kafka Deployment Engineering and opens with a bare table (`03-cluster.md:334-349`)
- [ ] Major: Dev: 780 words of Helm and kubectl procedures interrupt a Concept chapter (`03-cluster.md:195-288`)
- [ ] Major: Dev: the page shows no captured output although it tells the reader to read the cluster from the CLI (`03-cluster.md:355-396`)
- [ ] Major: Dev: 'a feature, not a bug' promises an outcome the book never measured (`03-cluster.md:119-123`)
- [ ] Major: Dev: the topology figure carries three ideas and repeats two tables; it renders as a thumbnail on phones (`03-cluster.md:47-81; /private/tmp/claude-501/-Users-bmscomp-codes-kates/3e573d47`)
- [ ] Minor: Fact: 'test both listeners to quantify the encryption cost' isn't possible without reconfiguring the Kates API (`03-cluster.md:302`)
- [ ] Minor: Fact: node IDs and pod suffixes are hardcoded three times but assigned by Strimzi (`03-cluster.md:57-68, 93-95, 268`)
- [ ] Minor: Dev: the handoff opens with the banned 'With X in place' pattern and the opening uses the 'This chapter documents' formula (`03-cluster.md:5, 407`)
- [ ] Minor: Dev: the running example appears once; the matrix and the page-cache budget never meet the payments targets (`03-cluster.md:107-125, 166-181`)
- [ ] Minor: Dev: dedicated roles, 'no ZooKeeper' and availability-versus-durability are each said two or three times (`03-cluster.md:37, 83-85, 152, 160, 179-181, 401`)
- [ ] Minor: Dev: 'topology' in two H2 titles means node layout, which the terminology table reserves for the namespace layout (`03-cluster.md:43, 195`)
- [ ] Minor: Dev: CONCEPTS.md still says 'planned' for sections this page now has (`CONCEPTS.md:22, 26`)
- [ ] Minor: Dev: the chapter needs one more 'Try it' after the lag section (`03-cluster.md:33`)
- [ ] Minor: Dev: the acks=all figure draws replication as a push from the leader; Kafka followers fetch (`03-cluster.md:141-150`)
- [ ] Minor: Copy: future tense in five sentences (`03-cluster.md:3, 104, 123, 168`)
- [ ] Minor: Copy: bold used for emphasis, for table labels and for list labels (`03-cluster.md:83, 115, 123, 180, 187-191, 327-330`)
- [ ] Minor: Copy: five sentences over 40 words (`03-cluster.md:31, 117, 201, 261, 400`)
- [ ] Polish: Copy: 'deep', 'via', 'e.g.' and 'built-in' against the voice rules (`03-cluster.md:83, 99, 125, 187, 201, 330, 332, 353`)
- [ ] Polish: Copy: 'the Kind cluster' unglossed where the terminology table wants `panda` (`03-cluster.md:89, 265, 343`)
- [ ] Polish: Copy: two section links don't use the section's heading as link text (`03-cluster.md:343, 380`)
- [ ] Polish: Copy: Glossary terms first used in the objectives and at line 39 are linked only later (`03-cluster.md:9, 39, 302`)
- [ ] Polish: Copy: status symbols in table cells where the book elsewhere writes words (`03-cluster.md:310-315, 338-341`)
- [ ] Polish: Copy: H2 lead-in ends in a colon before an H3, and 'ensure:' introduces a list of facts (`03-cluster.md:97-101, 197`)
- [ ] Polish: Copy: the Operational Components table describes components the lab doesn't run in the voice of a warning (`03-cluster.md:323-332`)

### `04-performance-theory.md`

Scores: clarity 3, accuracy 2, structure 3, voice 3, completeness 2, examples 2.

**Critical — Fact: peak throughput is not 'the highest throughput in any sampling window'.** At `04-performance-theory.md:55`. "**Peak throughput** — highest throughput observed in any sampling window". MetricUtils.computeSummary takes results.stream().mapToDouble(TestResult::getThroughputRecordsPerSec).max() (kates/src/main/java/com/bmscomp/kates/util/MetricUtils.java:73-76): the maximum of the per-task rates over the whole run, with no time windows.

Fix: Replace lines 52-55 with prose: "Kates reports two throughput numbers per run. Average throughput is the mean of the rates its producer and consumer tasks each achieved, and peak throughput is the highest of those task rates — not a peak over time, so a burst inside a run doesn't show in it; the latency heatmap is where a moment inside a run becomes visible."

**Critical — Fact: Kates does not report P99.9 for a test run.** At `04-performance-theory.md:116`. "Kates reports the mean plus P50, P95, P99, P99.9, and Max for every test run." (also Summary line 283, table row line 113). computeSummary passes a literal 0 for p999LatencyMs (MetricUtils.java:113); TestResult has no p999 field; kates report show prints Average, P50, P95, P99, Max (cli/cmd/report.go:86-90) and kates report summary the same (report.go:140-149). P99.9 exists only as a Micrometer gauge (LatencyHistogram.java:67) and in the live BenchmarkStatus (NativeKafkaBackend.java:971).

Fix: Line 116: "Kates stores the mean, P50, P95, P99 and Max for every test run (`kates report show <id>` prints them); P99.9 exists only as a live Prometheus gauge while the run is in progress." Line 283: drop "P99.9". Add a "Reported by Kates" column to the percentile table with "Live gauge only" on the P99.9 row. Verify the gauge name against LatencyHistogram.registerGauge before publishing.

The other findings, by severity:

- [ ] Major: Fact: average throughput is not 'total records / total duration' (`04-performance-theory.md:54`)
- [ ] Major: Dev: the chapter never uses the running example (`04-performance-theory.md:233-277`)
- [ ] Major: Dev: the promised queueing model is missing, so the chapter is half its minimum length (`04-performance-theory.md:12-39`)
- [ ] Major: Dev: 'How many samples a percentile needs' is registered here but not taught (`04-performance-theory.md:104-116`)
- [ ] Major: Dev: the noise floor and the baseline commands are promised and absent (`04-performance-theory.md:217-251`)
- [ ] Major: Dev: 'What "Good" Looks Like' table is invented numbers with no source (`04-performance-theory.md:252-263`)
- [ ] Major: Dev: the latency-step table reports uncaptured numbers and a constant column (`04-performance-theory.md:80-91`)
- [ ] Major: Dev: the Try it is not a Try it and runs placeholders (`04-performance-theory.md:265-278`)
- [ ] Major: Dev: the handoff uses the banned 'With X in place' form (`04-performance-theory.md:289`)
- [ ] Major: Figures: the throughput-latency regimes flowchart fakes a curve (`04-performance-theory.md:21-39 (fig-perf-throughput-latency-regimes); shots/ch-0`)
- [ ] Major: Figures: the long-tail 'distribution' is a table drawn as a chain (`04-performance-theory.md:122-131 (fig-perf-long-tail-percentiles); shots/ch-04/f`)
- [ ] Major: Figures: the heatmap flowchart is the banned fake heatmap (`04-performance-theory.md:191-200 (fig-perf-heatmap-slices); shots/ch-04/fig-heat`)
- [ ] Major: Figures: the coordinated-omission sequence diagram clips its notes in print and floats away (`04-performance-theory.md:153-177 (fig-perf-coordinated-omission); PDF p.67-68; s`)
- [ ] Major: Copy: both callouts lack titles (`04-performance-theory.md:141, 265`)
- [ ] Minor: Dev: the Heatmaps section re-explains its home chapter (`04-performance-theory.md:187-215`)
- [ ] Minor: Dev: 'Statistical Significance' does no statistics and houses an unrelated section (`04-performance-theory.md:217`)
- [ ] Minor: Dev: the GC callout is a deployment limit squeezed into a five-sentence tip (`04-performance-theory.md:141-143`)
- [ ] Minor: Dev: two more Try it exercises belong on the page (`04-performance-theory.md:116 and 185`)
- [ ] Minor: Dev: Summary has six bullets and two run past 25 words (`04-performance-theory.md:282-287`)
- [ ] Minor: Dev: 'tail latency' is taught here without a registry row or Glossary entry (`04-performance-theory.md:118-139`)
- [ ] Minor: Dev: the chapter does not say how Kates shows each concept (`04-performance-theory.md:104-116, 181-185`)
- [ ] Minor: Copy: eleven bold-label bullets (`04-performance-theory.md:54-55, 99-100, 135-139, 212-213`)
- [ ] Minor: Copy: bold used as a highlighter in running prose and table labels (`04-performance-theory.md:16-17, 19, 91, 95, 106, 110-114, 142, 147, 189, 254`)
- [ ] Minor: Copy: units and number formatting are inconsistent (`04-performance-theory.md:50, 84-91, 99-100, 102, 110, 124, 129, 142, 176, 198, 2`)
- [ ] Minor: Copy: link text is not the target's title (`04-performance-theory.md:142, 183, 215`)
- [ ] Minor: Copy: lead-ins that end in a colon and say nothing to notice (`04-performance-theory.md:43, 52, 59, 97, 106, 133, 202, 210, 219, 233, 254`)
- [ ] Minor: Copy: the heatmap Q&A table quotes answers Kates never prints (`04-performance-theory.md:204-208`)
- [ ] Minor: Copy: the Multiple Runs paragraph is 107 words of mixed topics (`04-performance-theory.md:250`)
- [ ] Polish: Dev: the concurrency warning is repeated from the Part page (`04-performance-theory.md:250`)
- [ ] Polish: Figures: the acks=all sequence diagram carries a redundant trailing note (`04-performance-theory.md:61-78 (fig-perf-acks-all-latency-path); shots/ch-04/fig`)
- [ ] Polish: Copy: intensifiers and editorializing (`04-performance-theory.md:14, 19, 95, 124, 147, 187, 198, 219`)
- [ ] Polish: Copy: citation placed mid-clause (`04-performance-theory.md:183`)
- [ ] Polish: Copy: Glossary links at first use are missed or late (`04-performance-theory.md:3, 10, 102, 110, 283`)
- [ ] Polish: Copy: the shell loop is heavier than the book needs (`04-performance-theory.md:237-241`)
- [ ] Polish: Copy: 'the producer itself blocks' needs its number (`04-performance-theory.md:185`)

### `05-test-types.md`

Scores: clarity 3, accuracy 4, structure 2, voice 3, completeness 2, examples 2.

The other findings, by severity:

- [ ] Major: Dev: The chapter never uses the running example (`05-test-types.md:80-109, 294-306, 398-409, 484-527`)
- [ ] Major: Fact: CAPACITY results table is invented data (`05-test-types.md:329-337`)
- [ ] Major: Fact: LOAD healthy-range table states unlabelled thresholds (`05-test-types.md:113-119`)
- [ ] Major: Dev: Registered concept homes are missing from the page (`05-test-types.md:12-36 (Test Type Overview)`)
- [ ] Major: Dev: Under length and uneven per-type skeletons (`05-test-types.md:38-527`)
- [ ] Major: Dev: Three blocks re-explain concepts whose home is another page (`05-test-types.md:499-527, 529-546, 264-268`)
- [ ] Major: Fact: VOLUME omits the topic settings Kates imposes (`05-test-types.md:278-292`)
- [ ] Major: Fact: Configuration tables hide per-type defaults that change results (`05-test-types.md:66-76, 156-162, 205-210, 257-262, 288-292, 320-325, 390-396, 47`)
- [ ] Major: Figures: Four load-profile flowcharts fake a chart; ENDURANCE's is one box (`05-test-types.md:46-55, 137-146, 186-195, 232-241; shots/ch-05-test-types/fig-lo`)
- [ ] Major: Figures: The taxonomy diagram is a list and prints as a full page (`05-test-types.md:16-36; shots/ch-05-test-types/fig-taxonomy.png; PDF p.60 (file `)
- [ ] Major: Dev: No recognized Try it exercises; the one exercise is mis-formed and misplaced (`05-test-types.md:568-586`)
- [ ] Major: Copy: Every callout breaks the callout rules (`05-test-types.md:123, 264-268, 480-482, 564-566, 568-586`)
- [ ] Major: Copy: Opening sentence is ungrammatical and opens with a product description (`05-test-types.md:3`)
- [ ] Major: Web: Eight tables overflow the column at phone width and clip (`http://127.0.0.1:8765/05-test-types.html at 390 px, `main table`; shots/ch-05-te`)
- [ ] Major: Print: A table runs under the folio and rules print as stray lines (`PDF p.64 (file page 78); shots/ch-05-test-types/pdf/p0078.png`)
- [ ] Minor: Copy: History sentence about the old consumer group behaviour (`05-test-types.md:121`)
- [ ] Minor: Fact: INTEGRITY's planned length is twice its duration (`05-test-types.md:267, 470-478`)
- [ ] Minor: Fact: The repeated Litmus kill is not something Kates sets (`05-test-types.md:523, 527`)
- [ ] Minor: Fact: Trogdor reports no maximum latency on any produce row (`05-test-types.md:14, 413`)
- [ ] Minor: Copy: Eleven bold-label bullets in the When to Use lists (`05-test-types.md:59-62, 150-152, 199-201, 282-284`)
- [ ] Minor: Copy: Repeated headings produce unstable anchors and say nothing (`05-test-types.md:42, 57, 64, 111, 133, 148, 154, 164, 182, 197, 203, 212, 228, 2`)
- [ ] Minor: Copy: Eight bare headings over tables with no lead-in (`05-test-types.md:64, 154, 203, 255, 286, 318, 459, 468`)
- [ ] Minor: Copy: Terminology and unit slips (`05-test-types.md:72, 117, 119, 261, 282, 290, 508, 559`)
- [ ] Minor: Copy: Bold pseudo-headings and bold emphasis in prose (`05-test-types.md:40, 95, 131, 180, 226, 230, 274, 278, 294, 312, 343, 398, 419, `)
- [ ] Minor: Copy: Five sentences over 40 words (`05-test-types.md:121, 230, 481, 501, 565`)
- [ ] Minor: Copy: Summary bullets over length and a marketing adjective (`05-test-types.md:591, 594, 501`)
- [ ] Minor: Copy: Ignored-parameter rows clutter the configuration tables (`05-test-types.md:70-71, 260, 392-393, 477`)
- [ ] Minor: Figures: STRESS stages diagram is a three-by-three table drawn as boxes (`05-test-types.md:168-174; shots/ch-05-test-types/fig-stress-stages.png`)
- [ ] Minor: Figures: INTEGRITY flow uses glyphs for status and shows two of five verdicts (`05-test-types.md:425-455; shots/ch-05-test-types/fig-integrity.png; PDF p.70 (fi`)
- [ ] Minor: Dev: Scenario-file examples carry invented gate values (`05-test-types.md:106-108, 304-305, 406-408, 493-496`)
- [ ] Minor: Dev: The Scenario Files section duplicates the handoff's destination (`05-test-types.md:529-566, 596`)
- [ ] Polish: Copy: Glossary links not at first use (`05-test-types.md:371, 381, 478, 501, 527`)
- [ ] Polish: Copy: Inline quoted output and an INTEGRATION_CDC pointer (`05-test-types.md:3, 585`)

### `13-scenario-files.md`

Scores: clarity 3, accuracy 4, structure 2, voice 3, completeness 3, examples 2.

**Critical — Figure 6.1 renders a literal \n in its node labels.** At `13-scenario-files.md:31-33; screenshot /private/tmp/claude-501/-Users-bmscomp-codes-kates/3e573d47-33f7-49e7-b360-e999ba990c4d/scratchpad/shots/ch-13/fig1.png`. Source `P1[Scenario 1\nLOAD test]`; the rendered figure shows the text 'Scenario 1\nLOAD test', 'Scenario 2\nSTRESS test', 'Scenario 3\nINTEGRITY test' with the backslash-n visible.

Fix: Write the labels as `P1["Scenario 1<br/>LOAD test"]` (same for P2, P3), or shorten to `P1[LOAD]`, `P2[STRESS]`, `P3[INTEGRITY]` so no break is needed. Check the PDF page after the change.

The other findings, by severity:

- [ ] Major: Dev: The running example's home file, payments-scenarios.yaml, is missing from its home chapter (`13-scenario-files.md:235-324`)
- [ ] Major: Dev: The spec limits sit in a 200-word, three-paragraph callout instead of a Limits subsection with a table (`13-scenario-files.md:94-100`)
- [ ] Major: Dev: None of the three callouts has a title (`13-scenario-files.md:94,159,578`)
- [ ] Major: Dev: The page has no properly marked 'Try it', and the one exercise sits in the wrong place (`13-scenario-files.md:578-595`)
- [ ] Major: Dev: The Summary has seven bullets, several over 25 words, and carries flags (`13-scenario-files.md:599-605`)
- [ ] Major: Dev: The '--wait or nothing is checked' fact is stated six times, and 'exit 2, nothing starts' four times (`13-scenario-files.md:159,169,176-180,190,342,600`)
- [ ] Major: Dev: Sections are out of reading order: lint is used before it is taught, JSON Format is stranded, Examples precede Running (`13-scenario-files.md:235,326,404,426,450`)
- [ ] Major: Dev: The chapter sets thresholds everywhere but never says how to choose one; the registry promises that section here (`13-scenario-files.md:205-233,546-553`)
- [ ] Major: Dev: Three rule lists written as prose or code comments belong in tables (`13-scenario-files.md:99,388-391,468-471`)
- [ ] Major: Figure 6.1 shows three scenarios fanning out in parallel, which contradicts the --wait sequence it is captioned with, and its three lanes are a list (`13-scenario-files.md:21-44; /private/tmp/claude-501/-Users-bmscomp-codes-kates/3`)
- [ ] Major: Figure 6.2 wires only the P99 gate to the outcome and repeats the three gate tables as a diagram (`13-scenario-files.md:178-203; /private/tmp/claude-501/-Users-bmscomp-codes-kates`)
- [ ] Major: At phone width the Consumer Configuration table's Description column collapses because one cell holds 67 words (`13-scenario-files.md:126; http://127.0.0.1:8765/13-scenario-files.html #consumer`)
- [ ] Major: Fact: 'no test type reads numConsumers' and 'parallelProducers counts only for STRESS and CAPACITY' hold only on the native backend (`13-scenario-files.md:95,104,109,121,125`)
- [ ] Minor: Dev: Common Mistakes 1–3 restate lint rules just taught, and Mistake 5 calls documented behavior a mistake (`13-scenario-files.md:479-576`)
- [ ] Minor: Dev: Five bare headings and three one-row tables (`13-scenario-files.md:61,141,147,239,288`)
- [ ] Minor: Dev: Hand-numbered H3s in Common Mistakes (`13-scenario-files.md:483,497,511,525,555`)
- [ ] Minor: Dev: The opening is a definition plus a formula, and the 'why' is four bold-label bullets (`13-scenario-files.md:3-19`)
- [ ] Minor: Dev: Headings that say nothing: Output, Basic Execution, How Execution Works, Test Execution (`13-scenario-files.md:330,344,352,141`)
- [ ] Minor: Fact: durationSeconds's LOAD default is 600 s, but the table says 'per type' although it claims to show LOAD's stock values (`13-scenario-files.md:92,145`)
- [ ] Minor: Fact: The Output sample is invented, with made-up run IDs and a 210 ms P99 that nothing captured (`13-scenario-files.md:355-376`)
- [ ] Minor: Copy: Future tense, evaluative and marketing wording (`13-scenario-files.md:14,155,214,428,527,594`)
- [ ] Minor: Copy: Four sentences over 40 words, one of 66 (`13-scenario-files.md:99,342,394,396`)
- [ ] Minor: Copy: Two fenced lines over 90 characters (`13-scenario-files.md:335,389`)
- [ ] Minor: Copy: Code annotations use ← arrows and ✅/❌ glyphs (`13-scenario-files.md:492,506-508,519-522,536-537,542-543,568,575`)
- [ ] Minor: Copy: 'Kates' stands for the CLI, and once for the Kates API, where the terminology table wants each named (`13-scenario-files.md:187,189,527,557`)
- [ ] Minor: Copy: Glossary terms not linked at first use in running prose (`13-scenario-files.md:3,126,165,207`)
- [ ] Minor: Copy: Output blocks not introduced by 'Output:' and the apply sample mixes stderr and stdout with no reading guide (`13-scenario-files.md:195,355-376,460-464`)
- [ ] Polish: Fact: --junit is documented as an alias although the CLI hides it from help (`13-scenario-files.md:397`)
- [ ] Polish: Copy: Hyphenation and abbreviations against CMOS and the Google style guide (`13-scenario-files.md:5,14,303,513`)
- [ ] Polish: Copy: Bold 'Fix:' lead labels before each remedy (`13-scenario-files.md:487,501,515,529,559`)
- [ ] Polish: Copy: Unlinked 'see … below' cross-references and blank 'Required' cells (`13-scenario-files.md:63-65,82-88`)
- [ ] Polish: Copy: The warning callout runs to four sentences and the Try it callout holds a procedure (`13-scenario-files.md:159-161,578-595`)
- [ ] Polish: Dev: The handoff to the previous chapter is fine; the handoff out is one clause and the Lab's claim is unsupported (`13-scenario-files.md:607; 05-test-types.md (Summary, last paragraph)`)

### `10b-lab.md`

Scores: clarity 3, accuracy 3, structure 2, voice 3, completeness 2, examples 2.

The other findings, by severity:

- [ ] Major: Fact: duration table says 1,000,000 records run 1000 s; the code caps it at 840 s, and the snippet hides the cap (`10b-lab.md:244-259`)
- [ ] Major: Fact: the CSV column list omits error_rate (`10b-lab.md:212`)
- [ ] Major: Dev: the page never says that Producers only acts under STRESS, and that Records is then per producer (`10b-lab.md:64-74, 240-259`)
- [ ] Major: Dev: Partitions and Replication only apply the first time a type's topic is created; sweeping them later changes nothing (`10b-lab.md:72, 152-166`)
- [ ] Major: Dev: the Lab has no throughput target, so every P99 it shows is a saturation P99, and the page never says so (`10b-lab.md:3-14, 64-74`)
- [ ] Major: Dev: the page is a feature tour with no worked example on krafter; the one exercise ignores the payments question (`10b-lab.md:1-288`)
- [ ] Major: Dev: at 1,509 words the chapter is half the STYLE.md floor and should grow, not split (`10b-lab.md`)
- [ ] Major: Dev: the Try it is a bold first line in a callout, not an exercise title, so CI counts no exercise and the question is never asked (`10b-lab.md:261-277`)
- [ ] Major: Copy: all three callouts carry default titles (`10b-lab.md:180, 196, 261`)
- [ ] Major: Copy: the Summary has six bullets, each over 25 words, full of keys, flags and enum names (`10b-lab.md:281-286`)
- [ ] Major: Figures: the Layout figure is a hand-drawn mock-up that shows strings the TUI never prints, and its alt attribute is empty (`10b-lab.md:30; docs/book/assets/kates-lab-tui.png (from kates-lab-tui.svg); shot`)
- [ ] Minor: Fact: median mode does not record 'the middle throughput/P99 value' (`10b-lab.md:194`)
- [ ] Minor: Dev: the opening defines the product instead of posing the problem (`10b-lab.md:3`)
- [ ] Minor: Dev: the cost of a sweep (runs x 60 s minimum) lives 140 lines away from the section that incurs it (`10b-lab.md:97-99, 240-259`)
- [ ] Minor: Dev: the Keyboard Reference comes before anything it refers to, and two headings open on a bare table (`10b-lab.md:16-24, 43-62`)
- [ ] Minor: Copy: the handoff states an outcome the reader did not capture (`10b-lab.md:288`)
- [ ] Minor: Fact: the Keyboard Reference's Context column is wrong for most keys, and it omits three keys the help line shows (`10b-lab.md:45-62`)
- [ ] Minor: Dev: quitting mid-run cancels the server-side test, and quitting between runs leaves nothing running; the page says neither (`10b-lab.md:222-238`)
- [ ] Minor: Copy: two pieces of advice and one 'In practice' are uncaptured claims (`10b-lab.md:170, 181, 197`)
- [ ] Minor: Copy: the Glossary link on 'baseline' points the session file at a different concept, and three other Glossary links use the wrong text or place (`10b-lab.md:14, 70, 74, 170, 288`)
- [ ] Minor: Copy: 'warmup' in prose where the terminology table says 'warm-up' (`10b-lab.md:13, 53, 168, 170, 172, 178, 181, 197, 284`)
- [ ] Minor: Copy: 'Lab vs CLI' is a false opposition, and 'Lab' / 'the Lab' alternate throughout (`10b-lab.md:16, 3, 28, 78, 97, 99, 154, 170, 242, 281`)
- [ ] Minor: Copy: the `go` fence is outside the style sheet's tag list and shows CLI internals to the reader (`10b-lab.md:244-249, 238`)
- [ ] Minor: Copy: 'Esc' and the diagram disagree on where Esc works, and two state-diagram edges are missing (`10b-lab.md:80-95; shots/ch-10b-lab/diagram.png`)
- [ ] Minor: Web: the Keyboard Reference table overflows its column on a phone and clips the Action text (`http://127.0.0.1:8765/10b-lab.html #keyboard-reference table; shots/ch-10b-lab/m`)
- [ ] Minor: Dev: the Layout numbered list, Iteration Workflow and Live Progress say the same things three times (`10b-lab.md:36-39, 97, 101-109`)
- [ ] Minor: Dev: 'In Kates' tie-back is missing: the page never shows how to open an iteration's run outside the Lab (`10b-lab.md:99, 212`)
- [ ] Polish: Copy: bold labels in table first columns and bold emphasis in prose (`10b-lab.md:22-24, 70-72, 74, 97, 138, 181, 186, 194`)
- [ ] Polish: Copy: wording that falls short of the voice rules (`10b-lab.md:23, 58, 74, 134, 154, 156, 166, 186, 232`)
- [ ] Polish: Copy: ampersands, key names in headings and a centered numeric table (`10b-lab.md:136, 200, 222, 253-254`)
- [ ] Polish: Copy: the objectives bullet promises 'pinpoint the winner', and 'optimal' appears three times (`10b-lab.md:3, 12, 166, 276`)

### `06-chaos-theory.md`

Scores: clarity 3, accuracy 2, structure 2, voice 3, completeness 2, examples 1.

**Critical — Fact: test workloads cannot pick the KIP-848 group protocol.** At `06-chaos-theory.md:276`. "Kates test workloads can exercise either protocol via the per-test-type `group-protocol` setting (default: `classic`)." The property `kates.tests.<type>.group-protocol` is declared in kates/src/main/java/com/bmscomp/kates/config/TestTypeDefaults.java:62-320 and exported by HealthResource.java:69, but `grep -rn groupProtocol kates/src/main/java` finds no other reader: TestOrchestrator.applyTypeDefaults (TestOrchestrator.java:1046-1080) copies every TypeConfig field except groupProtocol, TestSpec.java has no such field, and `group.protocol` is set only in ShareGroupConsumerService.java:166 (sha

Fix: Replace the sentence with the registry gloss: "Kates's test consumers run the classic protocol, the default of the Kafka client they run on, so a Kates test shows you the eager or cooperative pause, not the KIP-848 one." If the engine is meant to honour the property, that is a product fix, not a book fix; until it ships, say nothing about choosing the protocol.

**Critical — Fig: pipeline figure prints literal \n• in every node.** At `06-chaos-theory.md:302-308 (fig-chaos-gameday-pipeline); site #fig-chaos-gameday-pipeline; PDF p.111`. Node labels use "\n•" inside quoted flowchart strings (L303: P["1. Pre-flight\n• Cluster healthy\n• ..."]). Mermaid does not treat \n as a line break in a quoted label: the rendered node reads "1. Pre-flight\n• Cluster healthy\n• Backups verified\n• Team notified" on the site (shots/ch-06/fig-pipeline.png) and in print (shots/ch-06/pdf/p0125.png, PDF p.111). scripts/check-mermaid.sh passes because the syntax is valid.

Fix: Delete the figure and replace it with a table (see F-04) or, at minimum, rewrite each label with <br/> and no bullets: P["1. Pre-flight<br/>cluster healthy, team notified"]. Add a grep for '\\n' inside Mermaid node labels to scripts/check-mermaid.sh so it cannot recur.

The other findings, by severity:

- [ ] Major: Fact: the automated Game Day figure credits make gameday with steps the script does not run (`06-chaos-theory.md:296-316 (fig-cap, fig-alt and nodes of fig-chaos-gameday-pipe`)
- [ ] Major: Dev: two sections and two figures model the same Game Day (`06-chaos-theory.md:143-189 and 294-316`)
- [ ] Major: Dev: the page is under half the minimum length and its three registry homes are stubs (`06-chaos-theory.md (whole page); CONCEPTS.md:60-64`)
- [ ] Major: Fact: the leader-election timing table names the wrong setting and contradicts the home chapter (`06-chaos-theory.md:221-228`)
- [ ] Major: Fig: the Game Day methodology is a 15-node single-column chain (`06-chaos-theory.md:147-183 (fig-chaos-gameday-methodology); PDF p.107`)
- [ ] Major: Fig: the fault taxonomy renders as an unreadable strip; it is a table (`06-chaos-theory.md:64-101 (fig-chaos-fault-taxonomy); site #fig-chaos-fault-taxo`)
- [ ] Major: Dev: the Example Hypothesis ignores the running example and uses banned voice (`06-chaos-theory.md:185-189`)
- [ ] Major: Dev: the Try it has no title, sits under the wrong section and shows no output (`06-chaos-theory.md:324-341`)
- [ ] Major: Fig: the ISR state machine has a redundant state and does not show the home concept (`06-chaos-theory.md:234-246 (fig-chaos-isr-state-machine)`)
- [ ] Major: Dev: the escalation ladder is not monotonic on krafter (`06-chaos-theory.md:133-141`)
- [ ] Major: Dev: the opening is a textbook definition, not a hook (`06-chaos-theory.md:3-5`)
- [ ] Minor: Dev: leader election is re-taught outside its home (`06-chaos-theory.md:195-228`)
- [ ] Minor: Fig: five of eight figures are thumbnails on a phone (`06-chaos-theory.md:23, 64, 133, 199, 252 (figs 8.1, 8.2, 8.3, 8.5, 8.7); site at`)
- [ ] Minor: Copy: hand-numbered principle headings (`06-chaos-theory.md:50, 60, 105, 119, 129`)
- [ ] Minor: Fig: the reactive-versus-proactive figure is an 11-node strawman (`06-chaos-theory.md:23-44 (fig-chaos-reactive-vs-proactive)`)
- [ ] Minor: Copy: lead-ins before figures and tables say nothing to notice (`06-chaos-theory.md:21, 62, 131, 145, 193, 197, 232, 250, 296`)
- [ ] Minor: Dev: Summary has six bullets and carries a command (`06-chaos-theory.md:345-350`)
- [ ] Minor: Copy: fenced lines over 90 characters (`06-chaos-theory.md:125-126`)
- [ ] Minor: Copy: terminology and wording slips, batched (`06-chaos-theory.md:7, 58, 124, 189, 221, 296, 314, 346`)
- [ ] Minor: Fact: 'leader election rate should equal the number of partitions on the failed broker' (`06-chaos-theory.md:289`)
- [ ] Minor: Fact: the chaos-provider sentence lists two of four providers (`06-chaos-theory.md:320`)
- [ ] Minor: Dev: the automation example schedules a test, not an experiment (`06-chaos-theory.md:119-127`)
- [ ] Minor: Print: floats drift and a code block collides with the folio (`PDF p.107, p.108, p.111`)
- [ ] Polish: Copy: bold first-column labels and bold values in tables (`06-chaos-theory.md:228, 284-290`)
- [ ] Polish: Copy: unit spacing and number style are inconsistent (`06-chaos-theory.md:16, 187, 225-228`)
- [ ] Polish: Copy: a section link carries the chapter's title as its text (`06-chaos-theory.md:141`)
- [ ] Polish: Copy: bold used for emphasis and for a defined term (`06-chaos-theory.md:21, 145, 187, 276`)
- [ ] Polish: Dev: the production-likeness table asserts what it cannot know (`06-chaos-theory.md:109-115`)
- [ ] Polish: Copy: trailing-whitespace lines inside Mermaid fences (`06-chaos-theory.md:35, 76, 84, 92, 158, 165, 172, 178, 209, 217, 261`)
- [ ] Polish: Copy: 'general-purpose chaos tools don't understand' is marketing by contrast (`06-chaos-theory.md:193`)

### `07-chaos-practice.md`

Scores: clarity 3, accuracy 4, structure 2, voice 3, completeness 3, examples 2.

The other findings, by severity:

- [ ] Major: Dev: Split the page at Disruption Types; it is past the 8,000-word line (`07-chaos-practice.md:92-228 and :1`)
- [ ] Major: Dev: Formula opening; no hook, no running-example promise (`07-chaos-practice.md:3-5`)
- [ ] Major: Dev: The running example's two plans are missing from their home chapter (`07-chaos-practice.md:537-675 (Safety Guardrails) and :761-773`)
- [ ] Major: Dev: The page promises a report, timeline and kafka-metrics and never shows one (`07-chaos-practice.md:8, :696-718, :808-821`)
- [ ] Major: Dev: Kafka Intelligence and CI/CD Integration are stubs, and Kafka Intelligence duplicates Targeting Pods (`07-chaos-practice.md:676-718 and :761-773`)
- [ ] Major: Dev: Model after limits: the architecture figure and the resilience-run limits precede the model (`07-chaos-practice.md:12-58, :72-90, :775-806`)
- [ ] Major: Dev: Second Try it exercise is untitled, misplaced, repeats the first and is a procedure (`07-chaos-practice.md:905-926`)
- [ ] Major: Dev: Summary has six bullets, one of 48 words, with flags and commands (`07-chaos-practice.md:930-935`)
- [ ] Major: Copy: Two callouts with bold first lines and no title; the product bug lacks the Known limitation form (`07-chaos-practice.md:823-827 and :905-926`)
- [ ] Major: Copy: Repository internals and Java classes as sentence subjects (`07-chaos-practice.md:232, :477, :678, :722`)
- [ ] Major: Figures: fig-practice-architecture is a 20-node chapter map that prints unreadably small (`07-chaos-practice.md:16-58`)
- [ ] Minor: Dev: Six full playbook YAML blocks where a table and one file would do (`07-chaos-practice.md:230-451`)
- [ ] Minor: Dev: Conditional rules written as enumerated prose and a 10-bullet limits list (`07-chaos-practice.md:617 and :663-674`)
- [ ] Minor: Dev: Headings that say nothing or use the wrong term (`07-chaos-practice.md:12, :133, :150, :676, :829`)
- [ ] Minor: Copy: Bold emphasis in prose and superlatives without a measure (`07-chaos-practice.md:183, :236, :334, :682, :722, :831, :895, :933`)
- [ ] Minor: Copy: Sentences over 40 words (`07-chaos-practice.md:152, :191, :220, :293, :382, :891`)
- [ ] Minor: Copy: Fenced lines over 90 characters (`07-chaos-practice.md:362, :412, :493, :528`)
- [ ] Minor: Copy: Glossary terms unlinked at first use; UK spelling; future tense (`07-chaos-practice.md:68, :144, :206, :342, :408, :584`)
- [ ] Minor: Copy: Table cells over 25 words and a five-column table that clips on a phone (`07-chaos-practice.md:102-107, :143, :78, :571`)
- [ ] Minor: Figures: fig-practice-safety-guard is a decision table drawn as a 1,337 px tall flowchart, then repeated as a table (`07-chaos-practice.md:545-576`)
- [ ] Minor: Figures: fig-practice-sla-grade is three stacked lists with status glyphs (`07-chaos-practice.md:724-749`)
- [ ] Minor: Fact: Impact Analysis table shows words the CLI doesn't print (`07-chaos-practice.md:895-901`)
- [ ] Minor: Fact: 'node 0' in the split-brain section means broker 0 (`07-chaos-practice.md:293`)
- [ ] Polish: Dev: Handoff holds; previous page's promise matches (`07-chaos-practice.md:937`)
- [ ] Polish: Figures: fig-practice-az-failure carries its meaning in ✅/❌ and says less than its lead-in (`07-chaos-practice.md:340-358`)
- [ ] Polish: Figures: fig-practice-resilience-run would be better served by a real plot once a run is captured (`07-chaos-practice.md:833-864`)
- [ ] Polish: Figures: labels and captions audit of the remaining diagrams (`07-chaos-practice.md:168-181, :238-257, :295-311, :779-806`)
- [ ] Polish: Fact: Provider table's RBAC cell omits SCALE_DOWN on a StatefulSet-run Kafka (`07-chaos-practice.md:105 and :135`)
- [ ] Polish: Copy: SLA is glossed as SLO-style only at its tenth mention (`07-chaos-practice.md:10, :81, :722`)
- [ ] Polish: Copy: Bare lead-ins before the two field tables (`07-chaos-practice.md:455, :467`)

### `08-data-integrity.md`

Scores: clarity 3, accuracy 3, structure 2, voice 3, completeness 3, examples 2.

**Critical — Dev: The running example never arrives; the chapter uses Tutorial 4's file instead.** At `08-data-integrity.md:206-294, 567-583; part-chaos-integrity.md:35; CONCEPTS.md:145-146,169`. CONCEPTS.md:169 gives payments-integrity-broker.yaml and payments-integrity-zone.yaml their home in 'Data Integrity Verification', and part-chaos-integrity.md:35 tells the reader 'the verdicts come from two resilience files, payments-integrity-broker.yaml and payments-integrity-zone.yaml'. grep finds neither name anywhere in the chapter; the only worked file is integrity-chaos.yaml (line 211), which is the file docs/tutorials/04-integrity-under-fire.md:65 creates, on the default topic integrity-test with no consumerGroup and 180,000 records of default size. Neither payments-integrity nor payme

Fix: Make Integrity Under Chaos the home of payments-integrity-broker.yaml: show it in full with testRequest.spec {type INTEGRITY, numRecords, throughput 500, durationMs, recordSize 1024, acks all, replicationFactor 3, minInsyncReplicas 2, topic payments-integrity, consumerGroup payments-verify} and a chaosSpec identical to the broker step of payments-broker-loss.json (07-chaos-practice.md), steadyStateSec 30; keep the sizing table (lines 232-238) for it. Then show only the chaosSpec lines payments-integrity-zone.yaml changes (every Kafka pod in zone alpha, the selector payments-zone-loss.json uses), and state CONCEPTS' rule: start it only when the broker run is DONE because both share the topic 

**Critical — Fact: Figure 10.1 describes phases the INTEGRITY run does not have.** At `08-data-integrity.md:25-72 (fig-integrity-verification-pipeline); shots/ch-08/fig1-1440.png`. Lead-in line 25: 'An INTEGRITY run executes five sequential phases'. Nodes R1 'Wait for ISR recovery', R2 'Verify cluster health' and C1 'Consume all messages from beginning'. kates/src/main/java/com/bmscomp/kates/engine/NativeKafkaBackend.java has no ISR wait (grep ISR|recover: none); the recovery wait is ResilienceOrchestrator.awaitRecovery (resilience/ResilienceOrchestrator.java:181-200), and the page itself says at line 242 'The run's recovery wait does not change this sizing' and at line 478 the consumer reads 'from the first one written since the task was submitted' (NativeKafkaBackend.j

Fix: Redraw as one TB column of four cards: 'Produce: sequenced records, acks=all' -> 'Fault (optional, injected by kates resilience run while producing)' as a dotted side node -> 'Consume: read back from the run's first record' -> 'Verify: every acknowledged sequence read back?' with Yes -> PASS, No -> DATA_LOSS. Lead-in: 'An INTEGRITY run produces, reads back and reconciles; a fault, when there is one, lands during the produce phase:'. fig-cap: 'An INTEGRITY run produces sequenced records, reads them back from its first record, and counts every acknowledged sequence it never read as lost.' fig-alt: 'Four boxes top to bottom: Produce, Consume, Verify, Verdict, with an optional Fault box beside P

The other findings, by severity:

- [ ] Major: Fact: The objectives say the test exercises no transactions; a section tests them (`08-data-integrity.md:7`)
- [ ] Major: Dev: Restructure: the report is explained after it is used, and the Walkthrough repeats the chapter (`08-data-integrity.md:206-325, 441-583`)
- [ ] Major: Dev: The Integrity Modes callout carries the model; it should be prose and a table (`08-data-integrity.md:132-136`)
- [ ] Major: Dev: Standard and Idempotent Integrity are the same run (`08-data-integrity.md:138-166`)
- [ ] Major: Dev: Opening describes the chapter instead of hooking the payments reader (`08-data-integrity.md:3-21`)
- [ ] Major: Dev: Best Practices is a bare, hand-numbered list of generic advice (`08-data-integrity.md:408-439`)
- [ ] Major: Dev: No Limits subsection; limits are scattered across a callout, a paragraph and a parenthesis (`08-data-integrity.md:203, 244, 292, 375, 459`)
- [ ] Major: Fact: The timeline example lists one sequence as both CRC-failed and lost (`08-data-integrity.md:314-323`)
- [ ] Major: Dev: Step 5's 'Reading this report' invents causes from synthetic gaps (`08-data-integrity.md:553-557`)
- [ ] Major: Fact: Figure 10.2 draws a column where its text says a row, and is a list (`08-data-integrity.md:106-128; shots/ch-08/fig2-1440.png, shots/ch-08/m-fig2.png`)
- [ ] Major: Fact: Figure 10.3 is too wide for the site and clipped in print (`08-data-integrity.md:248-271; shots/ch-08/fig3-1440.png; PDF p.152 (physical 166`)
- [ ] Minor: Dev: Summary bullets run to 49 words and carry field names (`08-data-integrity.md:587-591`)
- [ ] Minor: Dev: The handoff uses the banned 'With X in place' formula (`08-data-integrity.md:593`)
- [ ] Minor: Dev: One Try it, in the wrong place, repeating the section it should follow (`08-data-integrity.md:567-583`)
- [ ] Minor: Dev: 'What Gets Verified' restates the verdict sentence and the producer counters (`08-data-integrity.md:296-304`)
- [ ] Minor: Dev: The disruption table names risks acks=all does not have (`08-data-integrity.md:416-426`)
- [ ] Minor: Fact: The field table's Duplicates row contradicts the chapter's default (`08-data-integrity.md:510, 512`)
- [ ] Minor: Dev: The causes table's 'How to Diagnose' column holds causes, not checks (`08-data-integrity.md:377-384`)
- [ ] Minor: Web: Three tables scroll sideways on a phone, hiding their last column (`http://127.0.0.1:8765/08-data-integrity.html table (Step 4, Common causes, Timel`)
- [ ] Minor: Copy: Callouts have no title attribute (`08-data-integrity.md:132, 202, 567`)
- [ ] Minor: Copy: 'message' and 'record' alternate for the same thing (`08-data-integrity.md:5, 21, 30, 34, 36, 54, 76, 97, 98, 104, 109, 124, 140, 301,`)
- [ ] Minor: Copy: 'ACK', 'ACKed', 'ACKs' in prose (`08-data-integrity.md:30, 35, 98, 100, 140, 300, 301, 304, 406, 478, 511, 516, 55`)
- [ ] Minor: Copy: CI-counted furniture: bare headings, missing lead-ins, bold-label bullets, bold table labels, emoji (`08-data-integrity.md:86, 97-100, 113-120, 296-304, 408, 445, 504-519, 561-565`)
- [ ] Minor: Copy: Three sentences over 40 words (`08-data-integrity.md:135, 170, 242`)
- [ ] Minor: Copy: Evaluative and marketing phrasing (`08-data-integrity.md:3, 146, 208, 377, 511, 513`)
- [ ] Polish: Copy: Terminology slips: records/s and a UK spelling (`08-data-integrity.md:215, 244, 258`)
- [ ] Polish: Copy: KIP-98 cited twice; leader unlinked at first use (`08-data-integrity.md:146, 150, 170, 240`)
- [ ] Polish: Copy: A parenthetical paragraph and a stray 'even during single-broker failures' (`08-data-integrity.md:347, 373`)

### `09-observability.md`

Scores: clarity 3, accuracy 2, structure 3, voice 3, completeness 3, examples 2.

**Critical — Fact: RTO/RPO row and its four alerts are described as impossible, but the integrity gauges are now published.** At `09-observability.md:267, 271-277, 650-652`. Line 267: 'Collapsed, and it cannot fill'; line 271: 'The RTO / RPO / data-integrity row does not work, and it never has'; line 273: 'nothing in this repository publishes any of them'; line 275: 'They are never registered with Micrometer'; line 651: the four alerts 'install cleanly and can never fire'. The code: kates/src/main/java/com/bmscomp/kates/engine/BenchmarkMetrics.java:553-589 registers kates.integrity.result.producer.rto.seconds, consumer.rto.seconds, max.rto.seconds, rpo.seconds, data.loss.percent, lost.records and duplicate.records, lazily on the first verified poll; charts/monitor

Fix: Replace lines 271-277 (the blockquote) with one callout: ::: {.callout-note title="The RTO / RPO row fills only for a run that verified integrity"} The six panels read the kafka:chaos:* recording rules, which record the kates_integrity_result_* gauges the Kates API registers when a run's integrity verifier reports. A plain LOAD run publishes none of them, so the row stays blank; blank during a resilience run means the verifier has not finished or the Kates ServiceMonitor is not scraped. The meters go with the run's others when it ends, so read the row while the run is in flight or widen the time range to it. ::: Change table row 267 to 'Producer, consumer and max RTO, RPO, data-loss percent;

The other findings, by severity:

- [ ] Major: Fact: the PrometheusRule group names in the Alerting table do not exist (`09-observability.md:646`)
- [ ] Major: Fact: the three alert YAML blocks are not 'as the chart renders them' (`09-observability.md:660-726`)
- [ ] Major: Fact: alert thresholds are called hard-coded, but alerts.thresholds is a chart value (`09-observability.md:660`)
- [ ] Major: Fact: the monitoring chart is pinned at 1.3.0; it is 1.7.1, and versions belong in the matrix (`09-observability.md:735-742`)
- [ ] Major: Fact: 'There is no output-file flag' — kates report export has -f/--file (`09-observability.md:467, 597`)
- [ ] Major: Dev: kates report heatmap, the Part IV artifact, never appears; the heatmap section shows no heatmap (`09-observability.md:408-503`)
- [ ] Major: Dev: the running example is absent; every worked number is invented (`09-observability.md:65, 79, 209, 492, 523-528, 548-555, 816`)
- [ ] Major: Dev: about 1,400 words of repository history and refactor arithmetic are contributor material (`09-observability.md:126, 128-139, 152, 167-177, 239, 251-253, 279, 289, 658, 761`)
- [ ] Major: Dev: Monitoring Stack Deployment is a task section inside a guide chapter and duplicates the part page and the Deployment Guide (`09-observability.md:733-801`)
- [ ] Major: Dev: zero Glossary links on the home page of five Glossary terms (`09-observability.md:3, 5, 59, 71, 73, 87, 91, 95, 150, 197, 387, 642`)
- [ ] Major: Dev: all five callouts lack titles, and the Try it is a nine-command procedure in the wrong section (`09-observability.md:99, 175, 617, 650, 803-829`)
- [ ] Major: Figures: the heatmap-pipeline diagram is a five-box list; a real heatmap would serve the reader (`09-observability.md:418-437; shots/ch-09/fig2-1440.png`)
- [ ] Minor: Fact: the kates cluster watch display list does not match what the command prints (`09-observability.md:397-404`)
- [ ] Minor: Fact: the kates trend output block is invented; the CLI prints a sparkline, a direction and a point count (`09-observability.md:520-528`)
- [ ] Minor: Fact: the diff table shows invented numbers and words the CLI does not print (`09-observability.md:546-555`)
- [ ] Minor: Fact: http://localhost:30086 is not reachable from the host on Kind without a port-forward (`09-observability.md:630`)
- [ ] Minor: Fact: the Kates metrics reference omits the seven integrity gauges and the backend tag (`09-observability.md:297-310`)
- [ ] Minor: Dev: the Summary has seven bullets with paths, flags and a version number (`09-observability.md:835-841`)
- [ ] Minor: Dev: the second opening paragraph is the banned 'This chapter covers' formula (`09-observability.md:5`)
- [ ] Minor: Dev: four bare headings and three tables without a lead-in, one of which contradicts its prose (`09-observability.md:494-496, 621-623, 744, 778-780`)
- [ ] Minor: Dev: six facts are stated twice on the page (`09-observability.md:59/469, 83/165, 169/761/837, 219/324-325, 271-279/651, 785 v`)
- [ ] Minor: Dev: the Kates Metrics Reference and Export Formats Summary sit mid-narrative and the export section repeats itself (`09-observability.md:293-341, 452-469, 584-597`)
- [ ] Minor: Figures: the architecture diagram has fifteen nodes, crossing edges and a caption that describes rather than states (`09-observability.md:20-57; shots/ch-09/fig1-1440.png`)
- [ ] Minor: Copy: bold-label pseudo-tables and bullets throughout the dashboard tour and alert section (`09-observability.md:189, 207, 223, 245, 257, 283, 201, 219, 235, 269, 399-402, 6`)
- [ ] Minor: Copy: fifteen horizontal rules and one hard-wrapped paragraph (`09-observability.md:14, 61, 103, 179, 291, 342, 406, 504, 530, 557, 582, 599, 63`)
- [ ] Minor: Copy: fenced lines over 90 characters and output blocks without 'Output:' (`09-observability.md:524, 676, 685, 725, 809; 475, 484, 522`)
- [ ] Minor: Copy: future tense, marketing adjectives and 'messages' / 'records/sec' terminology (`09-observability.md:100, 237, 755; 185, 259, 410, 534; 196, 213, 304, 305, 335, `)
- [ ] Minor: Copy: headings that say nothing, 'Step N:' colons, and a lowercase heading (`09-observability.md:67, 77, 85, 93, 128, 167, 281, 348-375, 471, 605`)
- [ ] Minor: Web: the Dashboard Set table repeats 'charts/monitoring' in all twelve rows and clips at phone width (`09-observability.md:109-122; http://127.0.0.1:8765/09-observability.html #the-da`)
- [ ] Polish: Dev: STYLE.md's 'a Java class is never the subject' is broken in the metrics reference and the heatmap section (`09-observability.md:299, 322, 329, 425, 441`)

### `deploying-strimzi-operator.md`

Scores: clarity 3, accuracy 3, structure 3, voice 3, completeness 3, examples 4.

The other findings, by severity:

- [ ] Major: Fact: the hook does not require 'exactly ten' CRDs, and the quoted failure line does not exist (`deploying-strimzi-operator.md:75 and :630`)
- [ ] Major: Fact: kates deploy does not install 'through the hook' — it disables the hook and applies the CRDs itself (`deploying-strimzi-operator.md:468`)
- [ ] Major: Fact: a newer installed operator is kept, not refused; what is refused is a requested downgrade (`deploying-strimzi-operator.md:468`)
- [ ] Major: Fact: 'kates clusters add' does not exist, and the chapter links a plan document (`deploying-strimzi-operator.md:468`)
- [ ] Major: Dev: the model section CONCEPTS.md assigns to this page (How the Operator Works) is missing (`deploying-strimzi-operator.md:14 (before 'What the Chart Deploys')`)
- [ ] Major: Dev: the migration section is written for the repository's one-time move, not for a reader who owns a Strimzi install (`deploying-strimzi-operator.md:191-196, :284, :309, :462-464, :584, :592`)
- [ ] Major: Dev: contributor material in Upgrading the Operator (six pins, check-versions.sh, dependency build) (`deploying-strimzi-operator.md:474-494, :117, :652-654`)
- [ ] Major: Dev: every callout is untitled, four H2s carry more than one, and three callouts hold procedures or run past three sentences (`deploying-strimzi-operator.md:77, :89, :101, :131, :145, :167, :283, :345, :372,`)
- [ ] Major: Dev: the Summary has seven bullets of 30-60 words carrying flags and paths (`deploying-strimzi-operator.md:656-664`)
- [ ] Major: Dev: the Try it is in the wrong place, in the wrong form, and wraps a procedure in a callout (`deploying-strimzi-operator.md:534-566`)
- [ ] Major: Dev: the Values table is 16 rows of 27-40-word cells and overflows the column on desktop and phone (`deploying-strimzi-operator.md:173-189; http://127.0.0.1:8765/deploying-strimzi-o`)
- [ ] Major: Copy: history and changelog phrasing in eleven places (`deploying-strimzi-operator.md:16, :134, :146, :177, :193, :284, :309, :485, :584`)
- [ ] Major: Copy: zero Glossary links although every term has an anchor (`deploying-strimzi-operator.md:3, :10, :16, :90`)
- [ ] Minor: Fact: the chapter assumes Helm 4 while the book's own toolchain pins Helm 3.17 (`deploying-strimzi-operator.md:75, :168`)
- [ ] Minor: Dev: Step 3's three movements are bold-label paragraphs that Rollback refers to by name (`deploying-strimzi-operator.md:349, :384, :415`)
- [ ] Minor: Dev: the handoff paragraph uses the forbidden 'With X in place' form (`deploying-strimzi-operator.md:666`)
- [ ] Minor: Dev: the same facts are stated two to four times across the page (`deploying-strimzi-operator.md:487 and :494; :474 and :654; :78, :105 and :632; :`)
- [ ] Minor: Dev: Prerequisites contradicts its own warning and omits the real prerequisites (`deploying-strimzi-operator.md:97-105`)
- [ ] Minor: Dev: two reader needs are missing — the namespace-scoped install and safe decommissioning (`deploying-strimzi-operator.md:129, :468, :502, :531`)
- [ ] Minor: Dev: fig-op-two-releases is sound but shouts and wraps by hand; a second figure for the three movements would help (`deploying-strimzi-operator.md:32-52; shots/ch-strimzi/fig-1440.png`)
- [ ] Minor: Copy: Scope is a bold blockquote (`deploying-strimzi-operator.md:5`)
- [ ] Minor: Copy: 23 fenced lines over 90 characters, seven of them the same jsonpath (`deploying-strimzi-operator.md:217, :232, :233, :258, :274, :304, :306, :320, :33`)
- [ ] Minor: Copy: sentences of 40-76 words (`deploying-strimzi-operator.md:3, :16, :75, :267, :269, :464, :468, :471, :563, :`)
- [ ] Minor: Copy: bold used for emphasis in prose and table cells (`deploying-strimzi-operator.md:26, :69, :70, :78, :127, :176, :179, :195, :284, :`)
- [ ] Minor: Copy: bold-label bullets in two lists (`deploying-strimzi-operator.md:430-432, :521, :528`)
- [ ] Minor: Copy: Troubleshooting headings are 60-110-character error strings that overflow the TOC (`deploying-strimzi-operator.md:572, :580, :588, :596`)
- [ ] Minor: Copy: code blocks without a lead-in, and a doubled lead-in before an Output block (`deploying-strimzi-operator.md:137, :299-301, :489`)
- [ ] Polish: Fact: 'thirteen of seventeen resources' and the crd-install label cannot be verified from the repository (`deploying-strimzi-operator.md:26, :208, :409, :552`)
- [ ] Polish: Dev: objective 4 promises 'the one flag' and the chapter never names it (`deploying-strimzi-operator.md:12`)
- [ ] Polish: Copy: future tense and 'will' in prose (`deploying-strimzi-operator.md:277, :468`)
- [ ] Polish: Copy: hyphenation and spelling — 'ad-hoc', 'airgapped', 'co-located' (`deploying-strimzi-operator.md:11, :183, :191, :193, :464, :468, :584`)
- [ ] Polish: Copy: em-dash density — up to four per sentence (`deploying-strimzi-operator.md:16, :75, :117, :229, :267, :269, :346, :464, :468,`)

### `20-installation-guide.md`

Scores: clarity 3, accuracy 3, structure 2, voice 3, completeness 4, examples 3.

The other findings, by severity:

- [ ] Major: Dev: the page is 10,214 words and two chapters; split at Chart Architecture Deep Dive (`20-installation-guide.md:925`)
- [ ] Major: Dev: the opening is a formula opening with a bold Scope blockquote and two objective lists (`20-installation-guide.md:3-21`)
- [ ] Major: Dev: Prerequisites runs 900 words with monitoring, Kyverno and production detours before the chart is explained (`20-installation-guide.md:23-156`)
- [ ] Major: Dev: Step 2 re-teaches Deploying the Strimzi Operator, the previous chapter (`20-installation-guide.md:289-337`)
- [ ] Major: Dev: the monitoring callout is a nine-sentence procedure and the dashboards paragraph is chart history (`20-installation-guide.md:86-92`)
- [ ] Major: Dev: Step 7 carries connect-cluster 2.0's values reference, which belongs to the Connect chapters (`20-installation-guide.md:493-591`)
- [ ] Major: Dev: content repeated inside the page: test commands, tier lists, pool YAML, listener tables (`20-installation-guide.md:652-669`)
- [ ] Major: Dev: twenty bare headings, three verification H3s with no prose and no expected output for topics (`20-installation-guide.md:613`)
- [ ] Major: Dev: 34 callouts, nine under one H2, types misused (`20-installation-guide.md:1529`)
- [ ] Major: Copy: twenty table cells over 25 words, one of 81 (`20-installation-guide.md:1334`)
- [ ] Major: Copy: 32 of 34 callouts have no title (`20-installation-guide.md:59`)
- [ ] Major: Copy: no Glossary link on a page that is the home of five concepts (`20-installation-guide.md:9`)
- [ ] Major: Fact: the upstream Strimzi chart is not vendored under charts/strimzi-operator/charts (`20-installation-guide.md:303`)
- [ ] Major: Fact: two of the chart's policy names are wrong (`20-installation-guide.md:1336`)
- [ ] Major: Fact: topic and user counts disagree with the profile and with each other (`20-installation-guide.md:2096`)
- [ ] Major: Figures: fig-install-chart-deployed-resources has 22 nodes, renders as a thumbnail, and its alt text enumerates an incomplete list (`http://127.0.0.1:8765/20-installation-guide.html #fig-install-chart-deployed-res`)
- [ ] Major: Figures: fig-install-chart-resource-graph is a boxed list with no edges and a wrong count (`http://127.0.0.1:8765/20-installation-guide.html #fig-install-chart-resource-gra`)
- [ ] Minor: Dev: Kyverno is explained three times and one table describes another chart (`20-installation-guide.md:112-129`)
- [ ] Minor: Dev: Summary bullets exceed 25 words and carry commands and flags (`20-installation-guide.md:2130-2134`)
- [ ] Minor: Dev: the one Try it is misplaced, untitled and repeats Verification; three exercises belong by their concepts (`20-installation-guide.md:2112-2126`)
- [ ] Minor: Dev: headings that say nothing or oversell (`20-installation-guide.md:1436`)
- [ ] Minor: Dev: bullets that should be prose and tables that should be prose (`20-installation-guide.md:1022-1025`)
- [ ] Minor: Dev: no roll-back step and no expected output for the install itself (`20-installation-guide.md:466`)
- [ ] Minor: Copy: future tense (`20-installation-guide.md:7`)
- [ ] Minor: Copy: history and changelog phrasing, including a whole migration H3 (`20-installation-guide.md:1409`)
- [ ] Minor: Copy: marketing adjectives and unverifiable superlatives (`20-installation-guide.md:927`)
- [ ] Minor: Copy: terminology table: lowercase 'kates' as the product, K8s in prose cells (`20-installation-guide.md:407`)
- [ ] Minor: Copy: bold run-in labels used as headings and 13 bold-label bullets (`20-installation-guide.md:360`)
- [ ] Minor: Copy: sentences over 40 words (`20-installation-guide.md:589`)
- [ ] Minor: Copy: fenced lines over 90 characters (`20-installation-guide.md:618`)
- [ ] Minor: Copy: eighteen horizontal rules (`20-installation-guide.md:21`)
- [ ] Minor: Copy: version pins in the chapter, some not the repository's (`20-installation-guide.md:29`)
- [ ] Minor: Fact: chart versions in the text and diagram are stale (`20-installation-guide.md:345`)
- [ ] Minor: Fact: the certificate-expiry alert is not in the chart's PrometheusRule the callout points at (`20-installation-guide.md:1830`)
- [ ] Minor: Fact: 'standard-rw' is not a GKE StorageClass, and the test suite is not nine tiers (`20-installation-guide.md:60`)
- [ ] Minor: Figures: fig-install-network-policy-traffic-flow's edge labels overlap and the dashed meaning is stated only in prose (`http://127.0.0.1:8765/20-installation-guide.html #fig-install-network-policy-tra`)
- [ ] Minor: Figures: fig-install-tiered-storage-flow is generic Kafka, not the chart, and fig-install-observability-architecture repeats the PodMonitor table (`http://127.0.0.1:8765/20-installation-guide.html #fig-install-tiered-storage-flo`)
- [ ] Polish: Dev: the handoff paragraph uses the forbidden 'With X in place' form (`20-installation-guide.md:2136`)
- [ ] Polish: Copy: output blocks introduced as 'Expected output:' and fenced json (`20-installation-guide.md:605`)
- [ ] Polish: Copy: bold first-column labels, 'e.g.' and 'etc.' in prose (`20-installation-guide.md:31`)
- [ ] Polish: Copy: troubleshooting headings not in Title Case, error string in quotes (`20-installation-guide.md:2015`)
- [ ] Polish: Copy: link text that is not the target's title (`20-installation-guide.md:1535`)
- [ ] Polish: Copy: citation placed mid-clause and carrying a version (`20-installation-guide.md:1559`)

### `15-kafka-deployment.md`

Scores: clarity 3, accuracy 2, structure 2, voice 3, completeness 3, examples 2.

**Critical — Fact: Prometheus Alerts section documents the legacy config/kafka/kafka-alerts.yaml, not the alerts the chart renders.** At `15-kafka-deployment.md:546-595`. "The alerting rules in `kafka-alerts.yaml` cover six categories", then `KafkaRaftLeaderElectionRate` (> 0.5 elections/s for 5min), `KafkaRaftUncommittedRecords`, `StrimziOperatorDown`, `KafkaCertificateExpiringSoon`. The chart's PrometheusRule (charts/kafka-cluster/templates/prometheusrule.yaml:151-165) renders `KafkaRaftLeaderElections` (more than 3 elections in 15m) and `KafkaRaftUnknownVoters`; its disk alerts are free < 20%/10% (lines 226-241); it adds nine alerts the page omits (KafkaUnderMinIsrPartitions, KafkaOfflineLogDirectory, KafkaUncleanLeaderElection, KafkaNodesMissing, KafkaFence

Fix: Delete the six H3 tables. Replace the section body with one paragraph: "The `kafka-cluster` chart renders one PrometheusRule per cluster, grouped as availability, replication, kraft, performance, storage, consumers, cruise-control and slo; [PrometheusRule Alerts](20-installation-guide.md#prometheusrule-alerts) lists every alert with its threshold. The operator chart raises `StrimziOperatorDown`, `StrimziReconciliationsFailing` and the certificate-expiry alerts, because only it scrapes the operator." Keep what this chapter adds that the table does not: which decision each alert group defends (e.g. KafkaUnderMinIsrPartitions is the `min.insync.replicas: 2` decision failing). Update appendix-b-

**Critical — Fact: Drain Cleaner annotates with strimzi.io/manual-rolling-update, not strimzi.io/delete-pod-and-pvc, and a local-storage pod does not migrate.** At `15-kafka-deployment.md:528-535`. Text block: "Annotates pod with strimzi.io/delete-pod-and-pvc → … Pod migrates to another node with data intact". The wrapper chart's own template says the webhook annotates pods with `strimzi.io/manual-rolling-update` (charts/strimzi-operator/templates/drain-cleaner.yaml:97), and Chaos Engineering in Practice line 141 documents the same annotation. `strimzi.io/delete-pod-and-pvc` is Strimzi's annotation for deleting a pod and its PVC, the opposite of preserving data; with `local-storage-*` classes a broker cannot move nodes with its PVC.

Fix: Replace the text block with prose: "When a node is drained, the Drain Cleaner's admission webhook sees the eviction request for a Kafka pod, denies it, and annotates the pod `strimzi.io/manual-rolling-update=true`. The Cluster Operator then restarts that pod in its own rolling update, one broker at a time and only while every partition keeps `min.insync.replicas`. The pod is rescheduled where its volume allows: on `panda`'s local storage that is the same node once it is uncordoned." Link the first mention to the Glossary (`gl-drain-cleaner`).

The other findings, by severity:

- [ ] Major: Fact: node-pool YAML and architecture figure quote the legacy raw manifests, not the chart or the values kates deploy generates (`15-kafka-deployment.md:131-208 and 64-98`)
- [ ] Major: Fact: wrapper chart version `0.3.0` is wrong, and the chapter should not pin a chart version at all (`15-kafka-deployment.md:16`)
- [ ] Major: Fact: two NetworkPolicy names in the table do not exist (`15-kafka-deployment.md:410-411`)
- [ ] Major: Fact: Backup section presents Velero as running by default, shows the wrong resource list, and points at a raw manifest (`15-kafka-deployment.md:597-616`)
- [ ] Major: Fact: rebalance workflow applies the raw manifest and names `full-rebalance`; the chart's resource is `krafter-full-rebalance` (`15-kafka-deployment.md:497-509`)
- [ ] Major: Fact: Strimzi reconciles into StrimziPodSets, not StatefulSets (`15-kafka-deployment.md:28`)
- [ ] Major: Fact: `num.io.threads` is a dynamic broker config; Strimzi applies it without a rolling restart (`15-kafka-deployment.md:53-58`)
- [ ] Major: Dev: the opening is a bold Scope blockquote and a formula paragraph, with no hook (`15-kafka-deployment.md:3-5`)
- [ ] Major: Dev: five tables and two paragraphs duplicate content whose home is another chapter (`15-kafka-deployment.md:276-311, 346-366, 368-377, 324-334, 546-595`)
- [ ] Major: Dev: the Summary breaks the length, flag and version rules in four of five bullets (`15-kafka-deployment.md:825-829`)
- [ ] Major: Dev: callouts — four untitled, two back-to-back pairs, a 219-word caution with a bold first line, four in one H2 (`15-kafka-deployment.md:414-420, 464-476, 652-665, 787-789`)
- [ ] Major: Dev: Kafka Broker Configuration gives YAML without the why the chapter promises (`15-kafka-deployment.md:210-272`)
- [ ] Major: Dev: fig-kde-deploy-flow has 13 nodes and renders as a 354 px tall ribbon (`15-kafka-deployment.md:622-640; shots/ch-15/fig-deploy-flow.png`)
- [ ] Major: Copy: zero Glossary links on a page that uses twenty-five Glossary terms (`15-kafka-deployment.md:5, 9, 10, 11, 28, 111, 127, 254, 291, 307, 352, 374, 409,`)
- [ ] Minor: Fact: `brokerCapacity.disk: 50Gi` is not in the chart (`15-kafka-deployment.md:486-493`)
- [ ] Minor: Fact: Cruise Control goal-mismatch fix lists 14 goals; the chart's `cruiseControl.goals` has 8 and owns the list (`15-kafka-deployment.md:791-815`)
- [ ] Minor: Fact: ConfigException entry fixes a key that is no longer anywhere, and the CrashLoopBackOff cause contradicts its own fix (`15-kafka-deployment.md:671-677 and 699-705`)
- [ ] Minor: Fact: users table understates two users' ACLs (`15-kafka-deployment.md:332-333`)
- [ ] Minor: Dev: the handoff uses the banned "With X settled, …" pattern (`15-kafka-deployment.md:831`)
- [ ] Minor: Dev: Cluster Architecture repeats The Cluster Under Test instead of building on it (`15-kafka-deployment.md:60-130`)
- [ ] Minor: Dev: Deploy Script Flow is repository internals; the reader runs `make kafka` or `kates deploy` (`15-kafka-deployment.md:618-650`)
- [ ] Minor: Dev: two troubleshooting entries carry a 700-word operator-reconcile procedure that belongs in Deploying the Strimzi Operator (`15-kafka-deployment.md:671-697 and 738-789`)
- [ ] Minor: Dev: Try it exercises — one exists, untitled; two more belong here (`15-kafka-deployment.md:652-665, after 311, after 420`)
- [ ] Minor: Dev: headings that say nothing or collide with other chapters (`15-kafka-deployment.md:422, 478, 252, 819, 161`)
- [ ] Minor: Dev: no Limits, and nothing on where krafter's numbers come from on a real cluster (`15-kafka-deployment.md (missing; after 208)`)
- [ ] Minor: Dev: fig-kde-cluster-architecture carries stale sizes in its node labels and repeats the physical-topology figure (`15-kafka-deployment.md:64-98; shots/ch-15/fig-cluster-arch.png`)
- [ ] Minor: Dev: fig-kde-operator-network-policy duplicates the table under it, and the listener figure duplicates its home chapter (`15-kafka-deployment.md:426-461 and 278-301; shots/ch-15/fig-operator-policy.png`)
- [ ] Minor: Copy: history and changelog phrasing in six places (`15-kafka-deployment.md:419, 424, 646, 703, 821, 402`)
- [ ] Minor: Copy: bold-label bullets and bold paragraph lead-ins throughout (`15-kafka-deployment.md:53-58, 200, 336, 362, 364, 366, 497, 644-648, 469, 673-67`)
- [ ] Minor: Copy: nine sentences over 40 words, one of 86 (`15-kafka-deployment.md:26, 364, 424, 480, 642, 664, 697, 765, 788`)
- [ ] Minor: Copy: link text that is not the target's title (`15-kafka-deployment.md:311, 415, 695`)
- [ ] Minor: Copy: terminology and spelling — British spelling, unbackticked krafter, 'topology' for node layout, superUser, 'the kates CLI' (`15-kafka-deployment.md:5, 62, 406, 458, 62, 642, 315, 330, 424, 133, 56, 55, 344`)
- [ ] Minor: Copy: tables — missing lead-ins, bold and symbols in cells, a 34-word cell, repeated Poor:/Good: prefixes (`15-kafka-deployment.md:102-107, 113-118, 124-129, 328-334, 350-361, 404-412, 453`)
- [ ] Polish: Dev: three `text` fences are ASCII flow diagrams that should be sentences (`15-kafka-deployment.md:338-342, 528-533, 721-723`)
- [ ] Polish: Dev: fig-kde-network-policy-union is the page's best figure; a captured listing beside it would prove it (`15-kafka-deployment.md:383-400; shots/ch-15/fig-netpol-union.png`)
- [ ] Polish: Copy: units — MB/GB in comments and tables where the values are MiB/GiB (`15-kafka-deployment.md:230, 231, 246, 247, 331, 332, 334, 364`)
- [ ] Polish: Copy: two fenced lines over 90 characters (`15-kafka-deployment.md:722, 753`)
- [ ] Polish: Copy: Scope line says 'listeners design', and a parenthetical repeats the heading's qualifier (`15-kafka-deployment.md:3, 422-424`)

### `12-deployment.md`

Scores: clarity 3, accuracy 4, structure 2, voice 3, completeness 4, examples 2.

**Critical — Figures: The Makefile target diagram renders as an unreadable 25-pixel strip.** At `http://127.0.0.1:8765/12-deployment.html #makefile-reference (figure 15.3); shots/ch-12-deployment/fig-makefile-reference-1440.png`. At 1440 px the SVG measures 1072×25 px and the site shows the thumbnail with the message 'Too wide to read here: expand it full screen.' (fig-makefile-reference-1440.png); at 390 px it is 291×7 px. Source :1037-1096: six `subgraph … direction TB` blocks side by side holding 36 target names and no edges — a list, which the Full Target List table (:1100) repeats. The PDF shrinks it to the page, so it prints as a line.

Fix: Delete the Mermaid block at :1033-1096 and its lead-in sentence's colon (:1031 ends 'from a single entry point.'). The merged table from D12-07 carries the grouping: add a first column 'Group' (Infrastructure, Kates, CLI, Testing, Chaos, Operations) if the grouping is wanted. Lower `mmd` for the page in the baseline.

The other findings, by severity:

- [ ] Major: Dev: The chapter is 8,795 words and three chapters; split it at two seams (`12-deployment.md:1 (whole page; seams at :180 and :790)`)
- [ ] Major: Dev: The opening is a formula and a bold Scope blockquote, not a hook (`12-deployment.md:3-16`)
- [ ] Major: Dev: Cloud Deployment sits between Resource Sizing and Quick Deployment (`12-deployment.md:180-523`)
- [ ] Major: Dev: The admin-less install procedure now lives on two pages (`12-deployment.md:899-1017; compliance.md:661-734`)
- [ ] Major: Dev: Security Configuration is about the Kafka cluster, outside the page's own scope (`12-deployment.md:1178-1219`)
- [ ] Major: Dev: The Makefile target table is cut in two by Native Image Build, and the diagram repeats it (`12-deployment.md:1098-1176`)
- [ ] Major: Dev: No Try it in the required form; the one exercise repeats the Quick Start and sits under Destroying the Environment (`12-deployment.md:1366-1389`)
- [ ] Major: Dev: Nine callouts, none titled; Cloud Deployment has three, Component-by-Component two (`12-deployment.md:60,174,188,460,466,742,759,1259,1366`)
- [ ] Major: Dev: Component-by-Component reads as a README: bare H3s over code blocks and fragments (`12-deployment.md:582-681`)
- [ ] Major: Copy: Glossary links missing at first use; six links, all in one section (`12-deployment.md:3,13,39,74,78,84,182,189,280,467,547,593,597,667,775,1186`)
- [ ] Major: Fact: The Go prerequisite is stale, and the table pins versions the matrix owns (`12-deployment.md:20-29`)
- [ ] Major: Fact: The audit log does record who acted (`12-deployment.md:1228`)
- [ ] Major: Fact: `kates deploy` does not run the Kates API 'at 1 CPU / 1Gi' (`12-deployment.md:168,730`)
- [ ] Major: Figures: The topologies diagram is a 1,482-pixel tower of three lists with a caption that states no point (`http://127.0.0.1:8765/12-deployment.html #deployment-topologies (figure 15.1); s`)
- [ ] Minor: Dev: Game Day Validation is a 14-word stub with no home here (`12-deployment.md:1263-1271`)
- [ ] Minor: Dev: Topologies diagram, Deployment Profiles table and Per-Component table overlap (`12-deployment.md:98-176`)
- [ ] Minor: Dev: Retention-sweep mechanics sit under an 'Architecture Decision' (`12-deployment.md:76-96`)
- [ ] Minor: Dev: Headings that confuse or say nothing (`12-deployment.md:37,98,149,549,630,982`)
- [ ] Minor: Dev: The three provider sections are parallel label-value prose; lead with one comparison table (`12-deployment.md:192-198,264-282,388-394`)
- [ ] Minor: Dev: Summary bullets run to 50 words and carry flags, paths and numbers (`12-deployment.md:1393-1397`)
- [ ] Minor: Copy: Future tense and conditional where the present is meant (`12-deployment.md:5,35,710,1230`)
- [ ] Minor: Copy: First person and intensifiers (`12-deployment.md:35,147,390,530,580,643,1128,1134,1236`)
- [ ] Minor: Copy: History phrases and a deprecated product name (`12-deployment.md:394,506,1207`)
- [ ] Minor: Copy: 'multi-namespace' where the book says 'isolated topology' (`12-deployment.md:37,39,61`)
- [ ] Minor: Copy: Eighteen sentences over 40 words (`12-deployment.md:184,196,272,392,486,488,519,730,740,775,862,984,1001,1008,1017,`)
- [ ] Minor: Copy: Bold-label bullets and bold run-in paragraph heads (`12-deployment.md:43-45,194-198,266-282,390-394,553,555,760,1130,1134,1136,1192-1`)
- [ ] Minor: Copy: Five fenced lines over 90 characters (`12-deployment.md:416,420,446,780,1380`)
- [ ] Minor: Copy: Output as comments, outputs described rather than shown, and arrows in prose (`12-deployment.md:1102,1155,1271,1388`)
- [ ] Minor: Copy: Link text is the chapter title where the target is a section (`12-deployment.md:45,74,189,760,771,788,841,1008,1205,1311`)
- [ ] Minor: Copy: Table rules — bold first-column labels, long cells, missing lead-ins (`12-deployment.md:20,51,70-72,151-155,888,892,1100,1158,1253`)
- [ ] Minor: Copy: Cite JEP 439 for generational ZGC; no citation on the page (`12-deployment.md:712,738`)
- [ ] Minor: Fact: '13 annotated methods' is 14, and the counts are contributor material (`12-deployment.md:689-706`)
- [ ] Minor: Fact: The Litmus log selector is not one the repository uses (`12-deployment.md:1350`)
- [ ] Minor: Fact: Startup, memory and build-time figures carry no source environment (`12-deployment.md:1134,1140-1141,1155`)
- [ ] Minor: Figures: The make all flowchart is a seven-step list drawn 1,076 pixels tall (`http://127.0.0.1:8765/12-deployment.html #quick-deployment (figure 15.2); shots/`)
- [ ] Minor: Web: Eleven of seventeen tables scroll horizontally at 390 px; the six-column sizing table is the worst (`http://127.0.0.1:8765/12-deployment.html #per-component-breakdown at 390 px; sho`)
- [ ] Polish: Copy: UK spellings (`12-deployment.md:91,1203`)
- [ ] Polish: Copy: An explicit id that equals the automatic slug (`12-deployment.md:549`)
- [ ] Polish: Fact: `make ports` honors MONITORING_NS; 'only in the kafka namespace' is the default, not the rule (`12-deployment.md:74`)

### `compliance.md`

Scores: clarity 4, accuracy 4, structure 3, voice 5, completeness 4, examples 4.

**Critical — Fact: the closing Limits say the check keeps no history, but the Kates API stores every report.** At `compliance.md:976 (contradicts compliance.md:128 and 853-929)`. Line 976: "The check never changes the cluster and keeps no history; the pipeline's logs and JUnit files are the record." Line 128: "The Kates API keeps every report, with who ran the check and when". kates/src/main/java/com/bmscomp/kates/api/ComplianceResource.java:112-120 stores each check, records an audit row and answers a Location header; 11-api-reference.md:682 says "Nothing deletes stored checks"; the whole Proving section depends on the stored check.

Fix: Replace the bullet with: "The check never changes the cluster. The Kates API stores every report, and the pipeline's JUnit files are a second copy of each; `kates evidence export` writes a period's checks, with the audit chain, for an auditor." Verify the evidence claim against cli/cmd/evidence.go:44-55 before merging.

The other findings, by severity:

- [ ] Major: Fact: the audit listing pages at 200, not 500, so 'page=1 and page=2 to reach 500' is wrong (`compliance.md:927`)
- [ ] Major: Dev: 1,660 words of install steps duplicate the Deployment Guide and push the chapter past its word budget (`compliance.md:572-810`)
- [ ] Major: Dev: the anchoring procedure uses raw curl on port 30083 while the chapter forwards 8080, and never names kates evidence export (`compliance.md:867-905`)
- [ ] Major: Dev: five captured outputs follow files the reader never saw a command run on (`compliance.md:198, 329, 357, 413, 552`)
- [ ] Minor: Dev: The Language at a Glance repeats the Compliance Language Reference, and its Limits list repeats Step 4 (`compliance.md:931-976`)
- [ ] Minor: Dev: two H3s are both titled Limits (`compliance.md:919, 965`)
- [ ] Minor: Dev: the Summary has seven bullets, one over 25 words and carrying a chart value (`compliance.md:980-986`)
- [ ] Minor: Dev: a third Try it would exercise the waiver lifecycle the chapter spends a figure on (`compliance.md:483 (after the waiver-expired output)`)
- [ ] Minor: Dev: the six 'Each rule in this section reads one thing' bullets are label–value pairs (`compliance.md:404-411`)
- [ ] Minor: Copy: Glossary terms unlinked at first use (`compliance.md:48, 126, 242, 407, 411, 587, 619, 657, 975`)
- [ ] Minor: Copy: explicit heading ids that are not GitHub slugs, and one step heading without the id its siblings have (`compliance.md:572, 585, 621, 659, 690`)
- [ ] Minor: Copy: one 66-word sentence, in a list item the metric doesn't count (`compliance.md:973`)
- [ ] Minor: Fact: the Settings table omits target.platform while claiming to list every setting (`compliance.md:937-945`)
- [ ] Minor: Figures: the placement figure is a three-row list drawn as a diagram; the captured pod listing would serve better (`compliance.md:286-304 (shots/ch-compliance/fig-placement.png)`)
- [ ] Minor: Figures: the drift figure's subgraph ignores its direction LR, and the prose points at 'the right of the figure' (`compliance.md:71-90 (shots/ch-compliance/fig-drift.png)`)
- [ ] Minor: Web: five tables are clipped at phone width, including the install comparison table (`http://127.0.0.1:8765/compliance.html main table (6th, 8th, 10th, 12th, 13th) — `)
- [ ] Minor: Dev: 'Running It in a Pipeline' shows no pipeline (`compliance.md:812-849`)
- [ ] Polish: Copy: British spelling and one future tense (`compliance.md:30, 406`)
- [ ] Polish: Copy: 'the cluster's Kafka cluster id', a comma splice and a hyphenation slip (`compliance.md:61, 369, 583`)
- [ ] Polish: Copy: the 503 output block is introduced without 'Output' (`compliance.md:763-768`)
- [ ] Polish: Figures: the status decision tree is 910 px tall with one edge running its full height (`compliance.md:134-148 (shots/ch-compliance/fig-status.png)`)
- [ ] Polish: Figures: the waiver and admin-less figures hold; one alt text says more than the figure draws (`compliance.md:464-475, 672-688 (shots/ch-compliance/fig-adminless.png)`)
- [ ] Polish: Dev: the hook and the handoff hold; the hook could preview the chapter's two captured gaps (`compliance.md:3-10, 988`)
- [ ] Polish: Copy: the objectives list mixes lowercase fragments with semicolons, unlike the next chapter's (`compliance.md:5-10`)
- [ ] Polish: Copy: an output is labelled with an environment the page only defines five screens later (`compliance.md:648, 801`)
- [ ] Polish: Copy: 'nothing for tests' undersells that the KafkaUser still writes to Kafka (`compliance.md:587-617`)
- [ ] Polish: Copy: the listener gloss names mTLS and SCRAM-SHA-512 without showing the values tls and scram-sha-512 the file uses (`compliance.md:393, 409`)

### `17-security.md`

Scores: clarity 3, accuracy 3, structure 2, voice 3, completeness 3, examples 2.

The other findings, by severity:

- [ ] Major: Fact: Kyverno mutate rule sets runAsUser 100, not 1000 (`17-security.md:860`)
- [ ] Major: Fact: user matrix and quota table omit the kates-mm2 user (`17-security.md:196-202, 809-814`)
- [ ] Major: Dev: split the chapter at Container Security (`17-security.md:659`)
- [ ] Major: Dev: the promised Securing Kates section is missing (`17-security.md:162, 393; CONCEPTS.md:97`)
- [ ] Major: Figure: policy-summary diagram is unreadable in print and duplicates the table (`17-security.md:569-604; PDF p.395; shots/ch-17-security/fig-policy.png, p0395.pn`)
- [ ] Major: Figure: threat model is a table drawn as a flowchart, and its mitigations are not in the chapter (`17-security.md:17-40; shots/ch-17-security/fig-threat.png`)
- [ ] Major: Dev: callouts carry no titles and break the per-H2 rules (`17-security.md:42, 113, 164, 204, 261, 312, 360, 388, 392, 531, 535, 816, 877, 1`)
- [ ] Major: Copy: Glossary terms unlinked at first use (4 links on a page that teaches 15 Glossary terms) (`17-security.md:3, 8, 10, 97, 106, 123, 162, 170, 198, 259, 318, 450, 822, 841, 8`)
- [ ] Major: Dev: the Try it exercise is a bold-first-line callout at the end, with none where the concepts are (`17-security.md:1123-1140, 657, 162`)
- [ ] Major: Dev: the hook is a formula opening and the chapter calls itself a reference (`17-security.md:3-5`)
- [ ] Major: Dev: the Cross-references callout renders as a run-on line in the PDF (`17-security.md:1082-1087; PDF p.410`)
- [ ] Major: Dev: production advice to 'use the tls listener' cannot be followed with the shipped users (`17-security.md:43, 97, 1144`)
- [ ] Minor: Fact: apicurio-registry row understates its grant (`17-security.md:200`)
- [ ] Minor: Fact: 'four ClusterPolicy resources' contradicts the ImageValidatingPolicy text (`17-security.md:841, 1041, 1079`)
- [ ] Minor: Dev: three pieces of content are stated twice on the page (`17-security.md:196-202 vs 809-814; 667-673 vs 858-864; 569-604 vs 606-613`)
- [ ] Minor: Dev: the Security Checklist is a task list of checkboxes (`17-security.md:1093-1102; PDF p.410`)
- [ ] Minor: Copy: the Summary has six bullets, each far over 25 words, with ports and commands (`17-security.md:1144-1149`)
- [ ] Minor: Copy: handoff uses the banned 'With X in place' form (`17-security.md:1151`)
- [ ] Minor: Copy: history and version pins in running prose (`17-security.md:129, 262, 477, 532, 731, 897, 899`)
- [ ] Minor: Copy: bold-label bullets and bold pseudo-headings (`17-security.md:450-451, 856, 866, 931-933, 1083, 1124`)
- [ ] Minor: Copy: UK spellings (`17-security.md:262, 617, 746, 1117`)
- [ ] Minor: Copy: fenced lines over 90 characters (`17-security.md:145, 644, 645, 646, 654`)
- [ ] Minor: Copy: sentences over 40 words (`17-security.md:160, 463, 519, 529, 539, 619, 1139`)
- [ ] Minor: Copy: brochure voice and value judgments in the older sections (`17-security.md:104, 170, 244, 340, 386, 822, 854, 883, 1049`)
- [ ] Minor: Dev: section order separates the Kafka model from its limits and parks Quotas under Container Security (`17-security.md:46, 370, 805`)
- [ ] Minor: Dev: three per-chart value tables are chart-reference material (`17-security.md:694-710, 752-765, 767-783`)
- [ ] Minor: Copy: table cells over 25 words (`17-security.md:424, 445, 739, 741, 744, 760, 773, 775, 777`)
- [ ] Minor: Copy: wide tables overflow the phone column (`17-security.md:94-98, 196-202; shots/ch-17-security/mob-table.png`)
- [ ] Minor: Copy: `krafter` unglossed and unbackticked at first prose use (`17-security.md:372, 219, 280`)
- [ ] Minor: Copy: bare heading, missing lead-ins and missing blank lines before fences (`17-security.md:194-196, 606, 302-303, 307-308`)
- [ ] Minor: Copy: an invented rationale for the litmus-chaos user's missing quotas (`17-security.md:816-818`)
- [ ] Minor: Figure: the ACL-model diagram restates one sentence (`17-security.md:180-188`)
- [ ] Minor: Figure: architecture overview has orphan nodes and buries its point (`17-security.md:50-84; shots/ch-17-security/fig-arch.png`)
- [ ] Minor: Dev: CONCEPTS.md registry rows for this page are stale (`CONCEPTS.md:94, 97`)
- [ ] Minor: Dev: chapter title promises compliance it does not cover, next to a chapter that does (`17-security.md:1; _quarto.yml (compliance.md precedes 17-security.md)`)
- [ ] Polish: Copy: heading needs an explicit id, and heading text that says little (`17-security.md:820, 852, 1054, 194, 565, 1104`)
- [ ] Polish: Copy: bold used for emphasis in running prose (`17-security.md:43, 114, 178, 192, 198, 269, 610, 638, 661, 822, 854, 883, 1045, `)
- [ ] Polish: Figure: Kyverno admission diagram shows mutation as a dead end (`17-security.md:824-837; shots/ch-17-security/fig-kyverno.png`)

### `19-multi-tenancy.md`

Scores: clarity 3, accuracy 4, structure 2, voice 3, completeness 2, examples 2.

**Critical — Fact/Figures: Figure 18.3 prints literal \n in every multi-line label, on the site and in the PDF.** At `19-multi-tenancy.md:313-323; http://127.0.0.1:8765/19-multi-tenancy.html #fig-mt-onboarding-workflow; PDF p.420 (folio 406); shots/ch-19/fig3.png, shots/ch-19/p`. Rendered nodes read '2. Set ACLs\n(prefix-scoped)', '7. Verify Access\n(produce/consume test)' and the diamond 'Verification\nPassed?' — the backslash-n is visible text. The page's other diagram uses <br/> and renders correctly. The figure also has 10 nodes in a TB chain, 1,380 px tall at 1440 px wide, and its 7 steps (user, ACLs, quotas, topics, credentials, network, verify) contradict the chapter's own Steps 1–5 (topics, user, network, configure, apply), while 'Debug & Retry' loops back to 'Create KafkaUser', which a declarative upgrade never repeats.

Fix: Delete the H2 'Tenant Onboarding Workflow' (lines 305–324) with its figure. If an overview figure is wanted, put a 5-node LR flowchart at the top of 'Onboarding a New Service' whose nodes are the chapter's steps in their corrected order (Define topics → Create the user → Allow network access → Apply and verify → Configure the service), with <br/> for line breaks, fig-cap 'One tenant is one block of chart values; the service is configured only after the upgrade has created its Secret.' and no decision diamond.

The other findings, by severity:

- [ ] Major: Dev: The worked example is `my-service`, not the running example's `payments` tenant (`19-multi-tenancy.md:7, 34-36, 90-147, 156-186, 421-430`)
- [ ] Major: Dev: 'How Quotas Work' — the concept's home section — does not exist; the sizing table stands in for the model (`19-multi-tenancy.md:227-258`)
- [ ] Major: Dev: The tenant concept's registry gloss — no prefix may begin another's — is never taught on its home page (`19-multi-tenancy.md:12-77`)
- [ ] Major: Dev: The opening is the 'This chapter covers' formula and never states the problem (`19-multi-tenancy.md:3`)
- [ ] Major: Dev: 'namespace-level isolation' names the Kafka prefix model with the Kubernetes word for something else (`19-multi-tenancy.md:14, 18, 491`)
- [ ] Major: Dev: Step 4 depends on Step 5 and the page tells the reader to go back (`19-multi-tenancy.md:154-157, 225`)
- [ ] Major: Dev: The same helm upgrade appears three times and two sections duplicate the onboarding steps (`19-multi-tenancy.md:194-207, 305-346, 433-441`)
- [ ] Major: Dev: Two monitoring H2s, separated by the workflow section, repeat one PromQL and one vague Grafana suggestion (`19-multi-tenancy.md:291-303, 348-410`)
- [ ] Major: Dev: The Alert Rules block pastes 20 lines of rendered YAML with 251-character lines to make one point; it overflows the column and the printed page (`19-multi-tenancy.md:388-408; PDF p.422 (folio 408); http://127.0.0.1:8765/19-mul`)
- [ ] Major: Dev: 'Try it' is untitled, placed after the handoff material, and bypasses the chapter's own method (`19-multi-tenancy.md:455-487`)
- [ ] Major: Dev: All three callouts are untitled and the Important one is chart history (`19-multi-tenancy.md:150-152, 209-211, 455`)
- [ ] Major: Dev: 'Partition Planning' is generic Kafka advice with unsourced numbers and no link to tenancy (`19-multi-tenancy.md:260-278`)
- [ ] Major: Figures: Figure 18.1 draws two ideas with fourteen nodes and a noun-phrase caption (`19-multi-tenancy.md:16-45; shots/ch-19/fig1.png; PDF p.414`)
- [ ] Major: Web: At 390 px the sizing table is clipped — the CPU and Use Case columns are cut off with no way to scroll (`http://127.0.0.1:8765/19-multi-tenancy.html main table:nth-of-type(2); shots/ch-`)
- [ ] Minor: Dev: The isolation matrix is the model and sits four sections after the Model heading; the Model has bold-label bullets instead (`19-multi-tenancy.md:47-52, 280-289`)
- [ ] Minor: Dev: Decommissioning leaves the copied Secret behind and gives the wrong reason to wait before deleting topics (`19-multi-tenancy.md:412-451`)
- [ ] Minor: Dev: The tenant's ACL grants Create on its topic prefix, which lets it bypass the chart's topic defaults (`19-multi-tenancy.md:124`)
- [ ] Minor: Dev: Page is 1,534 words, half the chapter floor, and what is missing is the model (`19-multi-tenancy.md`)
- [ ] Minor: Dev: Summary bullets carry flags and paths and run to 41 words (`19-multi-tenancy.md:491-495`)
- [ ] Minor: Dev: The handoff is the 'With X in place' formula and a stray see-also paragraph precedes it (`19-multi-tenancy.md:453, 497`)
- [ ] Minor: Dev: Step 3's two paragraphs bury the one rule that loses a tenant its access (`19-multi-tenancy.md:136-138`)
- [ ] Minor: Figures: Figure 18.2 is sound; its home is the missing quota model, and a captured throttle plot would serve better once a run exists (`19-multi-tenancy.md:242-256; shots/ch-19/fig2.png; PDF p.418`)
- [ ] Minor: Fact: 'rather than a ready-made percentage' is wrong — the vendored rules do export an idle-percent gauge, the lifetime mean (`19-multi-tenancy.md:369`)
- [ ] Minor: Fact: The sizing tiers are partly the chart's own tenants, but the table doesn't say so and its high tier disagrees with them (`19-multi-tenancy.md:231-236`)
- [ ] Minor: Copy: Bare headings, tables without a lead-in, and bold lead lines (`19-multi-tenancy.md:72, 227, 229, 231, 260, 262, 264, 271, 273, 280, 282, 352, 3`)
- [ ] Minor: Copy: No Glossary link on the page although fourteen terms with entries appear in running prose (`19-multi-tenancy.md:7, 8, 9, 52, 68, 156, 169, 188, 192, 222, 266, 289, 293, 340`)
- [ ] Minor: Copy: `krafter` unbackticked and unglossed; `panda` never named for the numbers (`19-multi-tenancy.md:3, 14, 18, 491`)
- [ ] Minor: Copy: Section links use the chapter title as link text instead of the section heading (`19-multi-tenancy.md:136, 156, 167`)
- [ ] Minor: Copy: Two figure captions are noun phrases, not the figure's point (`19-multi-tenancy.md:18, 311`)
- [ ] Polish: Fact: A client entry without `listeners` is admitted to every internal listener, which Step 3 does not say (`19-multi-tenancy.md:136, 147`)
- [ ] Polish: Copy: Sentences over 40 words (`19-multi-tenancy.md:56, 138, 410, 414`)
- [ ] Polish: Copy: Code blocks with no lead-in or no output; units and small terminology slips (`19-multi-tenancy.md:51, 114-116, 213, 251, 254, 258, 355, 360, 433`)
- [ ] Polish: Copy: Headings that repeat or misname their content (`19-multi-tenancy.md:103, 291, 326, 348`)

### `18-upgrade-playbook.md`

Scores: clarity 3, accuracy 4, structure 2, voice 3, completeness 3, examples 2.

The other findings, by severity:

- [ ] Major: Dev: the chapter is a Task chapter in STYLE.md's terms but keeps a Concept chapter's skeleton (`18-upgrade-playbook.md:3-8, 39-130, 527-536`)
- [ ] Major: Dev: the running example is absent; CONCEPTS.md assigns this chapter the re-test around an upgrade with kates report diff (`18-upgrade-playbook.md:58-69, 121-130, 364-377, 432-442, 469-475, 507-513`)
- [ ] Major: Dev: the chapter is the registered home of the two-versions concept, but the section does not exist (`18-upgrade-playbook.md:25-37, 94-102, 421, 444-446`)
- [ ] Major: Fact: the generic, corporate and production overlays of the kates chart do not pin an image tag (`18-upgrade-playbook.md:199`)
- [ ] Major: Dev: the page tells the reader to restore CRDs from a backup it never takes (`18-upgrade-playbook.md:41-54, 477-479, 522, 525`)
- [ ] Major: Fact: the Kyverno procedure ignores the release kates deploy installed and applies CRDs twice (`18-upgrade-playbook.md:261-312`)
- [ ] Major: Dev: rollback is told twice for Kafka, once for three components and never for the rest (`18-upgrade-playbook.md:132-134, 179-196, 247-259, 402-525`)
- [ ] Major: Dev: the opening is the banned formula and makes no promise a reader can test (`18-upgrade-playbook.md:3`)
- [ ] Major: Fact: the Rollback Decision Matrix states times and risk levels nothing measured (`18-upgrade-playbook.md:517-525`)
- [ ] Major: Copy: seven of eight callouts have no title, three H2s hold two, two stand back to back, and three exceed three sentences (`18-upgrade-playbook.md:96, 100, 144, 281, 342, 359, 444, 477`)
- [ ] Major: Copy: 27 bold-label step lines, a bold-label numbered list and a bold Golden rule paragraph (`18-upgrade-playbook.md:23, 41, 56, 71, 106, 119, 140, 142, 157, 267, 269, 285, 3`)
- [ ] Major: Copy: the Summary has eight bullets, carries flags and paths, and one bullet runs 70 words (`18-upgrade-playbook.md:527-536`)
- [ ] Major: Copy: thirteen fenced lines exceed 90 characters and overflow the PDF margin (`18-upgrade-playbook.md:113, 151, 152, 219, 231, 241, 275, 276, 277, 278, 339, 42`)
- [ ] Minor: Copy: the handoff uses the banned 'With X in place' form and misdescribes the next chapter (`18-upgrade-playbook.md:538`)
- [ ] Minor: Copy: no Glossary term is linked at first use, although the Glossary has entries for sixteen of them (`18-upgrade-playbook.md:5, 7, 29, 43, 73, 94, 104, 128, 155, 181, 332, 351, 355, `)
- [ ] Minor: Copy: krafter is never glossed on the page (`18-upgrade-playbook.md:47`)
- [ ] Minor: Copy: banned terms, history and filler phrases (`18-upgrade-playbook.md:12, 16, 166, 181, 263, 273, 285, 343, 404, 450, 478`)
- [ ] Minor: Dev: the API-migration step edits repository files the reader does not have (`18-upgrade-playbook.md:164-177`)
- [ ] Minor: Copy: three sentences over 40 words (`18-upgrade-playbook.md:145, 155, 199`)
- [ ] Minor: Copy: repeated headings, a bare H3 over a table, and an H2 that opens with a list (`18-upgrade-playbook.md:39, 138, 265, 132, 406, 430, 467, 505, 515-517, 380-382`)
- [ ] Minor: Copy: an emoji and bold text in table cells (`18-upgrade-playbook.md:520; PDF p.422`)
- [ ] Minor: Dev: the two checklists are GitHub task lists that render as disabled form controls (`18-upgrade-playbook.md:350-357, 382-388; http://127.0.0.1:8765/18-upgrade-playbo`)
- [ ] Minor: Dev: the pre-flight commands appear three times (`18-upgrade-playbook.md:58-69, 346-378, 364-375`)
- [ ] Minor: Dev: the only figure draws a generic pipeline the sentence above it already states (`18-upgrade-playbook.md:12-21; fig-1440.png; PDF p.411`)
- [ ] Minor: Dev: the Common Upgrade Issues table gives fixes a reader cannot run and one row that contradicts the page (`18-upgrade-playbook.md:390-400`)
- [ ] Minor: Dev: the exercise is mis-formed and repeats Step 2; two real Try its belong elsewhere (`18-upgrade-playbook.md:359-378`)
- [ ] Minor: Dev: the chapter pins versions in its own table and concedes the appendix wins (`18-upgrade-playbook.md:27-37`)
- [ ] Minor: Fact: the v1beta2 refusal is narrower than the page says (`18-upgrade-playbook.md:155`)
- [ ] Minor: Fact: UnsupportedVersionException is not shown to be the symptom of a version outside the operator's window (`18-upgrade-playbook.md:392, 396`)
- [ ] Minor: Fact: watching conditions[0] assumes the Ready condition is first (`18-upgrade-playbook.md:113, 427`)
- [ ] Minor: Copy: code blocks follow bold labels with no lead-in, and the page has no captured Output block (`18-upgrade-playbook.md:58, 108, 121, 159, 303, 425, 432, 452, 462, 469, 487, 494`)
- [ ] Minor: Web: three tables overflow the phone column and the matrix hides its Risk Level column (`http://127.0.0.1:8765/18-upgrade-playbook.html table (390 px); matrix-390b.png`)
- [ ] Minor: Dev: the Kates API upgrade asks for a chart version without saying how to find the one that matches the image (`18-upgrade-playbook.md:199-209`)
- [ ] Minor: Dev: Drain Cleaner and Monitoring are stub sections (`18-upgrade-playbook.md:179-196, 247-259`)
- [ ] Minor: Dev: the operator section sends a production reader elsewhere for the whole procedure (`18-upgrade-playbook.md:136-162`)
- [ ] Polish: Copy: section links use the chapter title instead of the section heading (`18-upgrade-playbook.md:142, 531`)

### `21-kafka-connect.md`

Scores: clarity 3, accuracy 4, structure 2, voice 3, completeness 3, examples 3.

The other findings, by severity:

- [ ] Major: Dev: The opening is a 'This chapter covers' formula that promises material the Scope then hands away (`21-kafka-connect.md:3-5`)
- [ ] Major: Dev: The Connect model (connector, task, worker group, rebalance) is never taught, although this page is its registered home (`21-kafka-connect.md:14-16, 260-262, 449, 481`)
- [ ] Major: Dev: Seventy lines of contributor material — the chart tree and the Makefile targets — sit in a reader chapter (`21-kafka-connect.md:68-117, 618-629`)
- [ ] Major: Dev: The Try it is a 30-line procedure inside a callout, parked at the end of the page instead of after the pipeline it exercises (`21-kafka-connect.md:1007-1041`)
- [ ] Major: Dev: Eleven callouts, none titled; three H2s carry two, and two sit back to back (`21-kafka-connect.md:157, 213, 256, 480, 506, 682, 762, 848, 906, 1001, 1007`)
- [ ] Major: Dev: Reference and operations blocks interrupt the concept flow, and the CLI Reference duplicates the CLI chapter (`21-kafka-connect.md:510-629`)
- [ ] Major: Dev: The Debezium change event is described four times but never shown (`21-kafka-connect.md:731, 747-758, 763, 1048`)
- [ ] Major: Copy: No Glossary links, although eleven of the page's terms have entries (`21-kafka-connect.md:3, 10, 12, 16, 70, 310, 477, 633, 698, 856`)
- [ ] Major: Fact: The EOS cost numbers ('~5% latency', '>100k rec/s') have no source and no environment (`21-kafka-connect.md:690-694`)
- [ ] Major: Fact: The Kind row says database egress goes to the `kates` namespace, but `kates deploy` overrides it to `--db-ns` (`database`) (`21-kafka-connect.md:566, 583, 79`)
- [ ] Major: Figures: The architecture figure draws three ideas with eleven nodes, duplicates the next chapter's first figure, and is a thumbnail on a phone (`21-kafka-connect.md:18-56`)
- [ ] Minor: Dev: Three sections are called 'Configuration', and five other headings say little (`21-kafka-connect.md:659, 798, 874, 119, 134, 469, 524, 591`)
- [ ] Minor: Dev: 1.x-to-2.x migration history is scattered through the chapter (`21-kafka-connect.md:89, 91, 158, 330, 443, 506-508`)
- [ ] Minor: Dev: Tables that should shrink or become prose: an empty Value column, a 16-row overlay matrix, and a 'Poor/Good' comparison with no takeaway (`21-kafka-connect.md:60-66, 471-478, 568-585`)
- [ ] Minor: Dev: Summary bullets exceed 25 words and carry a path and config keys (`21-kafka-connect.md:1045-1049`)
- [ ] Minor: Dev: 'Event Sourcing via CDC' conflates replaying a topic with a connector re-snapshot (`21-kafka-connect.md:952-981`)
- [ ] Minor: Copy: Five horizontal rules (`21-kafka-connect.md:696, 766, 852, 910, 1005`)
- [ ] Minor: Copy: Hand-numbered H4s, a lower-case H4, and a bold line used as a heading (`21-kafka-connect.md:363, 382, 399, 134, 939`)
- [ ] Minor: Copy: Bold-label bullets in the Schema Registry section (`21-kafka-connect.md:827-831`)
- [ ] Minor: Copy: Voice slips — future tense, marketing adjectives, 'The Kates platform', unbackticked krafter (`21-kafka-connect.md:16, 214, 589, 700, 1010, 683, 20`)
- [ ] Minor: Copy: Seventeen fenced lines over 90 characters (`21-kafka-connect.md:74, 79, 81, 89, 91, 96, 99, 105, 113, 114, 491, 559, 823, 82`)
- [ ] Minor: Copy: Bare headings and tables with no lead-in (ratchet counts) (`21-kafka-connect.md:58, 332, 469, 591, 602`)
- [ ] Minor: Fact: The `tasksMax` rationale is wrong in detail, and the chart does not enforce 1 (`21-kafka-connect.md:480-482, 1047`)
- [ ] Minor: Fact: The Outbox diagram asserts the outbox row 'is deleted after capture', which nothing in the shown configuration does (`21-kafka-connect.md:936, 941-950`)
- [ ] Minor: Fact: Ten plugin versions and the image tag are pinned in the chapter (`21-kafka-connect.md:195, 336, 338-349, 379, 394, 404`)
- [ ] Minor: Fact: The page omits the worker-level `schema.registry.url` on the ccompat path that `kates deploy` sets, which its own Schema Registry section calls wrong (`21-kafka-connect.md:566, 582, 835`)
- [ ] Minor: Figures: The source and sink flow diagrams are mirror images of one idea (`21-kafka-connect.md:268-290`)
- [ ] Minor: Figures: Three diagrams restate their lead-in sentence and add nothing (`21-kafka-connect.md:514-522, 706-716, 987-993`)
- [ ] Minor: Figures: Nine captions are noun phrases rather than the figure's point (`21-kafka-connect.md:20, 167, 270, 284, 416, 708, 778, 864, 922`)
- [ ] Minor: Figures: A captured dead-letter record would teach more than the DLQ flowchart (`21-kafka-connect.md:858-872`)
- [ ] Minor: Web: On a 390 px screen 29 blocks scroll sideways — eight tables and twenty-one code fences (`http://127.0.0.1:8765/21-kafka-connect.html at 390 px (shots/ch-21-kafka-connect`)
- [ ] Polish: Copy: Punctuation — arrows, 'e.g.', 'vs.', '>=' in prose (`21-kafka-connect.md:353, 616, 627, 628, 683, 760, 897`)
- [ ] Polish: Copy: Link text is not the target's title, and one tutorial is named by path instead of linked (`21-kafka-connect.md:158, 904, 1010`)
- [ ] Polish: Copy: Previous chapter's handoff describes this one as covering operations (`18-upgrade-playbook.md (final paragraph)`)
- [ ] Polish: Figures: The EOS sequence has a dangling 'Offsets Topic' lifeline, and the reconciliation sequence mixes two stories (`21-kafka-connect.md:639-655, 165-185`)

### `operating-kafka-connect.md`

Scores: clarity 3, accuracy 3, structure 3, voice 3, completeness 3, examples 2.

The other findings, by severity:

- [ ] Major: Fact: the jq path for `connectors -o json` is wrong (`operating-kafka-connect.md:414`)
- [ ] Major: Dev: the Summary contradicts the chapter on failover time (`operating-kafka-connect.md:788`)
- [ ] Major: Dev: the Throughput Benchmarks and Worker Sizing tables state numbers nobody measured (`operating-kafka-connect.md:124-139, 173-183`)
- [ ] Major: Copy: all seven callouts lack a title (`operating-kafka-connect.md:91, 225, 258, 378, 595, 640, 765`)
- [ ] Major: Copy: no Glossary link on the page (`operating-kafka-connect.md:3-16, 89, 168, 244, 304-306, 379, 455, 516, 542`)
- [ ] Major: Copy: 'Availability Zone' and 'AZ' where the book says 'zone' (`operating-kafka-connect.md:9, 14, 16, 23-31, 56, 58, 66, 70, 85, 86, 92, 788`)
- [ ] Major: Copy: history and chart-version narration throughout (`operating-kafka-connect.md:259, 264, 278, 280, 376, 379, 562, 593, 738`)
- [ ] Major: Dev: the Grafana Dashboard subsection is written for contributors (`operating-kafka-connect.md:262-288`)
- [ ] Major: Dev: the Try it exercise is misfiled, untitled, states an outcome and leaks a port-forward (`operating-kafka-connect.md:765-784`)
- [ ] Major: Dev: the concept the registry homes here has no section (`operating-kafka-connect.md:56-93, 546, 689`)
- [ ] Minor: Copy: the Scope is a bold blockquote (`operating-kafka-connect.md:5`)
- [ ] Minor: Figures: Task Distribution is a list drawn as a diagram and renders back to front (`operating-kafka-connect.md:145-164; screenshot shots/ch-operating-kafka-connect/`)
- [ ] Minor: Figures: a skull emoji marks the dead worker (`operating-kafka-connect.md:66; shots/ch-operating-kafka-connect/fig-az-failure.p`)
- [ ] Minor: Figures: the network-flows figure omits the default REST clients its caption promises (`operating-kafka-connect.md:333-362; shots/ch-operating-kafka-connect/fig-netflow`)
- [ ] Minor: Figures: the stretched-cluster figure routes the group protocol through connect-offsets (`operating-kafka-connect.md:18-48; shots/ch-operating-kafka-connect/fig-stretched`)
- [ ] Minor: Dev: 'the internal topics are the only persistent state' is said four times (`operating-kafka-connect.md:603, 640-642, 646, 790`)
- [ ] Minor: Dev: the Secret-grant story is told here and in the previous chapter (`operating-kafka-connect.md:479, 748-759`)
- [ ] Minor: Dev: the Summary has six bullets, four of them over 25 words, two carrying commands (`operating-kafka-connect.md:786-793`)
- [ ] Minor: Dev: 'How It Works' is three bold-label bullets under a heading that says nothing (`operating-kafka-connect.md:50-54`)
- [ ] Minor: Copy: five `---` rules (`operating-kafka-connect.md:185, 229, 520, 599, 655`)
- [ ] Minor: Copy: bold pseudo-headings and an uneven Symptom/Cause/Fix pattern in Troubleshooting (`operating-kafka-connect.md:166, 614, 663-754`)
- [ ] Minor: Copy: fenced line over 90 characters (`operating-kafka-connect.md:743`)
- [ ] Minor: Copy: headings that announce rather than say (`operating-kafka-connect.md:50, 203, 605, 627`)
- [ ] Minor: Copy: prose steps inside a bash fence and arrows in lists (`operating-kafka-connect.md:609-612, 631-638, 742`)
- [ ] Minor: Fact: the CDC test's phase list is incomplete and names the UI library (`operating-kafka-connect.md:302`)
- [ ] Minor: Fact: 'the production overlay sets all three' hides that the base values already do (`operating-kafka-connect.md:97-116`)
- [ ] Minor: Dev: Capacity Planning never mentions the chart's autoscaler (`operating-kafka-connect.md:118-183`)
- [ ] Minor: Dev: Helm Test sits under Observability (`operating-kafka-connect.md:231-315`)
- [ ] Minor: Web: the alerts table is unreadable at phone width (`http://127.0.0.1:8765/operating-kafka-connect.html #prometheus-alerts table; sho`)
- [ ] Minor: Dev: two more Try it exercises the page should have (`operating-kafka-connect.md:93, 518`)
- [ ] Polish: Copy: two sentences over 40 words (`operating-kafka-connect.md:282, 286`)
- [ ] Polish: Copy: vague and marketing wording (`operating-kafka-connect.md:3, 16, 171, 364, 384`)
- [ ] Polish: Copy: terminology slips in tables and callouts (`operating-kafka-connect.md:177, 490, 641, 651, 680`)
- [ ] Polish: Dev: the opening's second sentence is a 'this chapter covers' list (`operating-kafka-connect.md:3`)
- [ ] Polish: Copy: a table cell over 25 words and a citation in parentheses (`operating-kafka-connect.md:326, 89`)

### `22-mirror-maker2-migration.md`

Scores: clarity 4, accuracy 4, structure 2, voice 4, completeness 2, examples 2.

The other findings, by severity:

- [ ] Major: Dev: No Summary and no Try it in a concept chapter (`22-mirror-maker2-migration.md:228-263`)
- [ ] Major: Dev: Chapter is 1,351 words, under half the 3,000-word floor (`22-mirror-maker2-migration.md:1`)
- [ ] Major: Dev: The protocol floor is explained here although its home is the next chapter (`22-mirror-maker2-migration.md:68-84`)
- [ ] Major: Dev: The chart's values and presets are named but never shown (`22-mirror-maker2-migration.md:86-94,161-162`)
- [ ] Major: Fact: The pre-flight hook is presented as if it ran by default (`22-mirror-maker2-migration.md:88-90`)
- [ ] Major: Dev: The migration lab's report is described but never shown (`22-mirror-maker2-migration.md:218-220`)
- [ ] Major: Dev: 'Metrics That Answer Operational Questions' is a bare heading over a table with no lead-in (`22-mirror-maker2-migration.md:181-183`)
- [ ] Major: Dev: Limits are bold-label bullets under 'What This Does Not Give You' (`22-mirror-maker2-migration.md:228-238`)
- [ ] Major: Copy: All five callouts use the shorthand syntax and have no title (`22-mirror-maker2-migration.md:42,80,124,195,222`)
- [ ] Major: Copy: 129-character fenced line overflows the column on the site and is cut off in the PDF (`22-mirror-maker2-migration.md:110`)
- [ ] Major: Dev: The cutover is a procedure drawn as a flowchart; it should be an ordered list (`22-mirror-maker2-migration.md:131-150`)
- [ ] Major: Fact: The kafka-common paragraph is false from a checkout and reads as a changelog (`22-mirror-maker2-migration.md:92-94`)
- [ ] Major: Copy: Metric names and a jsonpath overprint neighbouring table columns in the PDF (`22-mirror-maker2-migration.md:168-172,183-189 (PDF p.487-488)`)
- [ ] Minor: Dev: 'Where to Go Next' mixes further reading, two operational helpers and a Make-target note (`22-mirror-maker2-migration.md:240-259`)
- [ ] Minor: Dev: Two callouts in one H2, and the Important callout carries model content (`22-mirror-maker2-migration.md:80-84,124-129`)
- [ ] Minor: Dev: Alias changes between the model (`legacy`) and the exclusion example (`source`) (`22-mirror-maker2-migration.md:29,55,106-107`)
- [ ] Minor: Dev: 'One refresh interval' is never defined (`22-mirror-maker2-migration.md:143,150`)
- [ ] Minor: Dev: The opening has a hook but no promise (`22-mirror-maker2-migration.md:3-12`)
- [ ] Minor: Copy: Bold used for emphasis and as paragraph labels (`22-mirror-maker2-migration.md:11,37-38,57,73,152,157`)
- [ ] Minor: Copy: Three sentences over 40 words (`22-mirror-maker2-migration.md:104,174,253`)
- [ ] Minor: Copy: No Glossary link on the page (`22-mirror-maker2-migration.md:3,6,116,177`)
- [ ] Minor: Copy: British spelling, unexpanded abbreviations and an informal 'MM2' (`22-mirror-maker2-migration.md:64,107,223,231`)
- [ ] Minor: Copy: The figure caption is a noun phrase, not the figure's point (`22-mirror-maker2-migration.md:137`)
- [ ] Minor: Dev: 'Three Uses' needs a takeaway that names the chart key, and 'Verifying a Mirror' needs the command it names (`22-mirror-maker2-migration.md:53-60,168-172`)
- [ ] Minor: Fact: `sync.group.offsets.enabled` is the chart's setting, not MirrorMaker's default (`22-mirror-maker2-migration.md:115-118`)
- [ ] Minor: Fact: The Strimzi 'Kafka 4.x only' claim has no home in the Version & Compatibility Matrix (`22-mirror-maker2-migration.md:203-205`)
- [ ] Minor: Dev: Verification and metrics come after the cutover they gate (`22-mirror-maker2-migration.md:131,164,181`)
- [ ] Polish: Copy: 'condition=Ready on the CR' is a kubectl fragment in a prose table (`22-mirror-maker2-migration.md:170`)
- [ ] Polish: Copy: Arrow glyphs as prose punctuation (`22-mirror-maker2-migration.md:77-78,213`)
- [ ] Polish: Copy: Handoff holds; one repeated verb (`22-mirror-maker2-migration.md:261-263`)

### `23-legacy-source-migration.md`

Scores: clarity 4, accuracy 4, structure 3, voice 4, completeness 3, examples 3.

The other findings, by severity:

- [ ] Major: Web: both tables are clipped at phone width; the 3.x and 4.x columns and the Cause text are unreadable (`http://127.0.0.1:8765/23-legacy-source-migration.html section#what-the-sources-e`)
- [ ] Major: Dev: a Concept chapter with no Summary (`23-legacy-source-migration.md:361`)
- [ ] Major: Copy: all four callouts have no title (`23-legacy-source-migration.md:38, 83, 187, 236`)
- [ ] Major: Dev: 2,170 words with no captured example of the tools the chapter promises (`23-legacy-source-migration.md:45-58, 213-216, 336-341`)
- [ ] Major: Dev: two bare headings over tables with no lead-in (`23-legacy-source-migration.md:94-96, 346-348`)
- [ ] Minor: Figures: the two-hop diagram collapses to a 291x36 px thumbnail on a phone and its caption is a label, not a point (`23-legacy-source-migration.md:69-76; screenshots /private/tmp/claude-501/-Users-`)
- [ ] Minor: Dev: the GetOffsetShell cliff is told twice (`23-legacy-source-migration.md:147-153, 174-180`)
- [ ] Minor: Dev: the opening describes the previous chapter before it states this one's problem (`23-legacy-source-migration.md:3-16`)
- [ ] Minor: Dev: no Try it exercises on a page that has two cheap ones (`23-legacy-source-migration.md:62, 344`)
- [ ] Minor: Fact: the comment on `kates migrate run` understates what it does (`23-legacy-source-migration.md:336-341`)
- [ ] Minor: Fact: the page never says that the chart's replication policy defaults to `default` while the CLI's defaults to `identity` (`23-legacy-source-migration.md:330-332, 354`)
- [ ] Minor: Fact: `requireDeclaredVersion` is off by default; "worth keeping on" implies it is on (`23-legacy-source-migration.md:60-62`)
- [ ] Minor: Fact: the Version & Compatibility Matrix does not hold "the exact pairing" the page sends the reader to (`23-legacy-source-migration.md:284-287`)
- [ ] Minor: Copy: bold-label bullets and bold-led paragraphs throughout (`23-legacy-source-migration.md:121, 124, 199, 204, 207, 225, 229, 231, 248, 255, `)
- [ ] Minor: Copy: no Glossary link on the page; nine terms are used without one at first use (`23-legacy-source-migration.md:4, 8, 10, 12, 34, 87, 121, 185, 252, 330, 333`)
- [ ] Minor: Copy: cross-references written as "see X below/above" and unlinked document names (`23-legacy-source-migration.md:41, 125, 251, 313, 323`)
- [ ] Minor: Copy: the whole file is hard-wrapped at about 80 columns (`23-legacy-source-migration.md:3-374 (every paragraph)`)
- [ ] Minor: Copy: the block quotation has no clear attribution (`23-legacy-source-migration.md:22-29`)
- [ ] Minor: Copy: `$SRC` is used in five commands and never defined (`23-legacy-source-migration.md:140, 144, 164, 317`)
- [ ] Minor: Copy: the era table has an empty header cell, no column ratios, inconsistent cell capitalization and a row nothing explains (`23-legacy-source-migration.md:96-104`)
- [ ] Minor: Print: the symptom table's rows run together with no row spacing (`PDF p.483 (What Goes Wrong); also p.478–479 (era table)`)
- [ ] Minor: Dev: the symptom table's right column mixes cause and remedy under one header (`23-legacy-source-migration.md:348-356`)
- [ ] Minor: Dev: the model (what MirrorMaker 2 requires) arrives after a long limits section (`23-legacy-source-migration.md:64, 94, 244`)
- [ ] Polish: Copy: future tense and a British spelling (`23-legacy-source-migration.md:233, 326`)
- [ ] Polish: Copy: heading not in Title Case after the colon (`23-legacy-source-migration.md:218`)
- [ ] Polish: Copy: five sentences over 40 words (`23-legacy-source-migration.md:10-13, 78-81, 201-203, 264-267, 277-280`)
- [ ] Polish: Copy: changelog phrases the ratchet counts (`23-legacy-source-migration.md:111-112, 183, 222-223`)
- [ ] Polish: Copy: "In this platform" twice, meaning the book's charts (`23-legacy-source-migration.md:257, 289`)
- [ ] Polish: Print: a ten-line YAML fence is split across a page break (`PDF p.477–478`)
- [ ] Polish: Copy: en dash spacing differs between the diagram and the prose (`23-legacy-source-migration.md:72, 74, 79`)
- [ ] Polish: Copy: the KIP-1124 quotation is cited two paragraphs before it appears (`23-legacy-source-migration.md:27-29, 78-79`)
- [ ] Polish: Dev: Where to Go Next repeats the previous chapter's links and omits this chapter's own hands-on material (`23-legacy-source-migration.md:361-369`)

### `14-recipes.md`

Scores: clarity 3, accuracy 2, structure 2, voice 3, completeness 3, examples 3.

**Critical — Fact: kates report diff 'Expected output' is not what the command prints.** At `14-recipes.md:64-77`. The block shows columns 'Metric | Baseline | New | Delta', rows for P99 Latency, Throughput, Error Rate and 'Data Loss', and a closing line '✓ All metrics within 10% tolerance'. cli/cmd/diff.go:66-121 prints Banner('Report Diff', 'a vs b') then a table with headers Metric | <id1> | <id2> | Delta | (▲/▼/=) over nine metrics (Avg Throughput, Peak Throughput, Avg MB/s, Avg/P50/P95/P99/Max Latency, Error Rate), then two hints ('Baseline: … │ Comparison: …', '▲ = improved │ ▼ = regressed │ = ≈ no change'). There is no Data Loss row and no tolerance anywhere in the command. The Summary (L740) repeat

Fix: Replace L64-77 with a capture of kates report diff on panda from two runs of the suite (label it 'Output, on panda:'), or if no capture is possible, rewrite to the real shape (nine-metric table with id columns and the ▲/▼/= indicator) labelled 'with illustrative numbers'. Delete 'against your tolerance' in the Summary and in L740; say instead 'read the ▼ rows, and decide against the tolerance you set in the scenario's validate block'.

**Critical — Fact: kates trend 'Expected output' is invented, including the 'anomaly' annotation.** At `14-recipes.md:154-172`. The block shows '▸ Trend: LOAD / p99LatencyMs (30 days, 30 runs)', 'Min: 8.2ms Max: 14.1ms Avg: 10.5ms', a sparkline and '↑ anomaly (run #26)'. cli/cmd/trend.go:151-233 prints a 'Trend Chart' sub-header, the sparkline followed by a direction arrow and '(N data points)', then Min/Max/Average as key-values, a 'Data Points' table (Run ID, Timestamp, Value, ▲/▼ marker beyond ±20% of baseline) and, when any, a '⚠ Regressions Detected' table (Run ID, Value, Baseline, Deviation). No 'anomaly' label exists. L172 then tells the reader to spot 'a sudden spike in the sparkline' when the Regressions table

Fix: Capture kates trend --type LOAD --metric p99LatencyMs --days 30 on panda after a week of the schedule (or run it against the lab's existing LOAD runs) and paste it; otherwise draw the real shape with illustrative numbers. Rewrite L172: 'A run in the Regressions Detected table, or a ▲ in the Data Points table, is the one to diff: kates report diff <previous-run> <that-run>.'

**Critical — Fact: Recipe 6 shows kates report compare printing a table that Recipe 5 (correctly) shows as JSON.** At `14-recipes.md:554-568`. L556-568: '▸ Comparison: 4 runs', a box table with Scenario/Throughput/P99 Latency/Error Rate and lines 'Best throughput: Full Optimization (28,100 rec/s, +97.9% vs Defaults)'. cli/cmd/report.go:156-169: output.Header("Comparison") then output.RawJSON(data). Recipe 5 (L421-461) shows exactly that JSON for the same command. Two recipes on one page contradict each other about one command.

Fix: Replace L554-568 with 'Output: the same JSON Recipe 5 reads, one runs entry per scenario' plus a hand-made Markdown table of the four runs labelled 'illustrative numbers' (Scenario | avgThroughputRecPerSec | p99LatencyMs), and point to Recipe 5's reading guide for the fields. Drop the 'Best throughput/Best latency' lines, or state them as the reader's conclusion from the table.

The other findings, by severity:

- [ ] Major: Dev: the chapter never delivers the krafter readiness sign-off CONCEPTS.md assigns to it (`14-recipes.md:1-749; CONCEPTS.md:187-193`)
- [ ] Major: Dev: Recipes 1-4 and 6 ignore the Task skeleton that Recipes 5 and 7 follow (`14-recipes.md:23-383, 507-572`)
- [ ] Major: Dev: five recipes have no verify, roll back or clean-up step (`14-recipes.md:23-113, 117-172, 176-305, 309-381, 507-572`)
- [ ] Major: Dev: the opening is a formula and promises the wrong thing (`14-recipes.md:3`)
- [ ] Major: Dev: Recipe 1 should re-test with payments-scenarios.yaml, and show the file before using it (`14-recipes.md:39-44, 83-113`)
- [ ] Major: Dev: every 'Expected output' number is unlabelled, and four of them are invented (`14-recipes.md:64, 154, 356, 554`)
- [ ] Major: Figures: both Mermaid figures render their labels with literal backslash-n (`14-recipes.md:36, 195-200; http://127.0.0.1:8765/14-recipes.html #fig-recipes-up`)
- [ ] Major: Copy: six callouts have no title, Recipe 3 has two in one H2 and one holds a procedure (`14-recipes.md:79, 168, 180, 303, 377, 570`)
- [ ] Major: Copy: bold-label pseudo-headings and 'Goal:' labels (`14-recipes.md:25, 39, 46, 52, 58, 119, 123, 138, 147, 178, 203, 209, 216, 226, 3`)
- [ ] Major: Web: the seven-row overview table is unreadable at phone width and loose in print (`http://127.0.0.1:8765/14-recipes.html main table; shots/ch-14-recipes/m390-table`)
- [ ] Minor: Dev: the Summary breaks the type and the bullet rules (`14-recipes.md:738-748`)
- [ ] Minor: Dev: material explained again outside its home chapter (`14-recipes.md:113, 340, 733-734`)
- [ ] Minor: Dev: Recipe 2 switches to the API's JSON vocabulary without saying so (`14-recipes.md:123-136`)
- [ ] Minor: Dev: Recipe 7's JUnit loop can be one flag (`14-recipes.md:647, 696, 707-712`)
- [ ] Minor: Dev: a 'Try it' belongs after Recipe 2 and one after Recipe 5 (`14-recipes.md:172, 481`)
- [ ] Minor: Figures: Figure 24.1 restates its four step headings and prints at caption size (`14-recipes.md:31-37; PDF p.486`)
- [ ] Minor: Figures: Figure 24.2 is a six-item list drawn as a flowchart (`14-recipes.md:190-201`)
- [ ] Minor: Figures: Recipe 5's ceiling deserves a plot from a captured series (`14-recipes.md:471-481`)
- [ ] Minor: Copy: five horizontal rules (`14-recipes.md:115, 174, 307, 383, 505`)
- [ ] Minor: Copy: repeated 'Procedure' headings and one bare heading (`14-recipes.md:27, 83, 121, 186, 313, 511`)
- [ ] Minor: Copy: four fenced lines over 90 characters overflow the column (`14-recipes.md:221, 222, 223, 370`)
- [ ] Minor: Copy: three sentences over 40 words (`14-recipes.md:46, 181, 304`)
- [ ] Minor: Copy: Glossary terms unlinked at first use (`14-recipes.md:5, 7, 8, 39, 113, 172, 226, 248, 304, 331, 381`)
- [ ] Minor: Copy: 'SLA' and 'gate' used before their glosses (`14-recipes.md:19, 178, 304, 481`)
- [ ] Minor: Copy: a code block with no lead-in and an unnamed file (`14-recipes.md:513-546`)
- [ ] Minor: Fact: the GC-log grep in Recipe 4's tip matches nothing on a modern JDK (`14-recipes.md:378`)
- [ ] Minor: Dev: 'Run time' column gives counts of runs, not times (`14-recipes.md:13-21`)
- [ ] Polish: Copy: output blocks not introduced by 'Output:' (`14-recipes.md:64, 154, 250, 356, 421, 554`)
- [ ] Polish: Copy: voice slips — 'optimal', 'likely', 'Practical, ready-to-use' (`14-recipes.md:3, 346, 509, 571`)
- [ ] Polish: Copy: link text carries the hand-typed numbers of another chapter's headings (`14-recipes.md:184`)
- [ ] Polish: Copy: unit and time style drifts (`14-recipes.md:17, 158, 181, 248, 338, 561-567`)

### `10-cli-reference.md`

Scores: clarity 3, accuracy 4, structure 2, voice 3, completeness 3, examples 3.

**Critical — Dev: 17,862-word page must split into a core reference plus four family references.** At `10-cli-reference.md:1-2910`. metrics-summary: 17862 words, 679 fenced lines, 46 tables, 134 H4 entries. Rendered page is 121,856 px tall at 1440 px (about 135 screens) and 167,049 px at 390 px; the right-hand TOC lists 183 entries (toc-depth 4). STYLE.md Page Furniture: 'Aim for 3,000-6,000 words per chapter, and split one that passes 8,000.'

Fix: Split into five pages, keeping file names as stable ids and adding each to _quarto.yml after part-reference.md: (1) 10-cli-reference.md 'CLI Reference' keeps lines 1-321 (purpose line, new 'How to Read an Entry', Installation, Configuration, Global Flags, the Commands map table) plus Output Modes, Exit Codes and Shell Completion (2821-2899), about 4,500 words; (2) new cli-tests-reports.md 'CLI Reference: Tests, Reports and Analysis' takes Test Commands, Report Commands, Trend Analysis, Schedule Commands, Observability & Monitoring, Interactive Lab, Analysis & Optimization, Tuning, Profile, Cost, Snapshot, Flow, Badge, Webhook (713-1381, 2205-2601), about 5,500 words; (3) new cli-cluster-chao

**Critical — Fact: the hero figure is a mock-up of commands and values the CLI does not have.** At `10-cli-reference.md:16 (docs/book/assets/kates-cli-overview.png); http://127.0.0.1:8765/10-cli-reference.html main img`. The PNG shows 'kates --help' listing commands export, broker, consumer-groups and dlq, a Backend of 'native-java', a Status of 'COMPLETED', 'Producers 4' for a LOAD run and an 'SLO Gate' line. cli/cmd/root.go registers no export, broker, consumer-groups or dlq command (the AddCommand list has advisor ... webhook); the page itself says LOAD runs one producer (line 745) and run statuses are DONE/FAILED (line 728); the benchmark backend is 'native' (line 757). Rendered alt text is empty: the bracket text becomes the figcaption and img.alt is ''.

Fix: Delete assets/kates-cli-overview.png. Replace it with a captured terminal screenshot (or a text block) of the real 'kates' banner and 'kates test create --type LOAD --records 100000 --wait' output on panda against krafter, cropped to the text column, saved as assets/kates-cli-run.png, and write it as ![](assets/kates-cli-run.png){fig-alt="Terminal: kates test create for a LOAD run of 100,000 records on krafter, ending DONE with its throughput and P99 rows"} with a caption sentence that states the figure's point ('A LOAD run on panda: the command, the run ID, and the produce row you read the targets from'). If no capture is available, remove the figure: a reference page needs no hero image.

The other findings, by severity:

- [ ] Major: Dev: no Reference skeleton: missing 'How to Read an Entry', entries follow no template (`10-cli-reference.md:7-16, 717-731, 993-999, 1028-1050, 1348-1363, 2385-2418`)
- [ ] Major: Dev: 'Common Workflows' is guide material that duplicates Recipes & Patterns and ignores the running example (`10-cli-reference.md:38-142`)
- [ ] Major: Dev: the 'deploy' entry is a 1,272-word essay that belongs in Deployment Guide (`10-cli-reference.md:1387-1476`)
- [ ] Major: Copy: all fourteen callouts lack a title attribute and render as bare 'Note', 'Warning', 'Important' (`10-cli-reference.md:29, 140, 604, 762, 768, 1181, 1449, 1470, 1727, 2455, 2607, `)
- [ ] Major: Dev: two back-to-back callouts under 'test create', one holding a 300-word procedure with three code blocks (`10-cli-reference.md:762-793`)
- [ ] Major: Fact: 'kates ports' no longer stops every port-forward on the machine; line 1588 contradicts line 1592 and the code (`10-cli-reference.md:1588, 1592`)
- [ ] Major: Fact: run IDs 't-a1b2c3', 'abc123', 'def456' do not match the 8-hex-character IDs the Kates API issues (`10-cli-reference.md:52, 58, 61, 967, 1089-1090, 2239-2241, 2246, 2391, 2417`)
- [ ] Major: Dev: help-text mismatches are described in prose instead of 'Known limitation' callouts or fixes (`10-cli-reference.md:602, 946, 1283`)
- [ ] Major: Fact: existing commands and flags are missing from the reference (`10-cli-reference.md:216, 284-319, 2426-2447, 2109-2131, 2411-2418, 2782-2794`)
- [ ] Major: Dev: Migration Commands is a narrative with bold emphasis, history and sentence-case headings, not reference entries (`10-cli-reference.md:1657-1731`)
- [ ] Major: Dev: conditional rules written as paragraphs should be tables (`10-cli-reference.md:195, 837, 898, 1026, 1594, 2847`)
- [ ] Major: Copy: 24 table cells over 25 words (`10-cli-reference.md:306, 407, 747, 753, 892, 938, 939, 940, 941, 942, 1408, 1413`)
- [ ] Minor: Dev: the 'Common Workflows' and command-family intros duplicate other pages and each other (`10-cli-reference.md:437, 2038, 1385, 1389, 2227, 2231`)
- [ ] Minor: Dev: eight parent-command stubs are headings with five words under them (`10-cli-reference.md:439-441, 2040-2046, 2333-2339, 2379-2383, 2426-2432, 2459-24`)
- [ ] Minor: Dev: Summary breaks the 3-5 bullet, 25-word, no-flags rule and the Reference type (`10-cli-reference.md:2901-2910`)
- [ ] Minor: Copy: repository internals and contributor paths in a reader-facing reference (`10-cli-reference.md:20-27, 1422, 1450, 1659, 1672, 2610, 2835`)
- [ ] Minor: Copy: history sentences say what the CLI used to do (`10-cli-reference.md:189, 193, 634, 1659, 1672, 2274, 2300, 2666`)
- [ ] Minor: Copy: marketing adjectives and evaluative asides (`10-cli-reference.md:106, 112, 496, 507, 954, 1058, 1737, 2314`)
- [ ] Minor: Copy: 37 sentences over 40 words, two over 70 (`10-cli-reference.md:84, 286, 632, 715, 765, 837, 898, 946, 1058, 1202, 1262, 132`)
- [ ] Minor: Copy: no Glossary link on a page that uses twenty Glossary terms (`10-cli-reference.md:66, 146, 187, 329, 393, 471, 612, 727, 758, 765, 1114, 1163,`)
- [ ] Minor: Copy: bold-label paragraphs, bold-label bullets and bold emphasis instead of headings and prose (`10-cli-reference.md:152, 161, 431, 626, 948, 1052, 1108, 1219, 1287, 1340, 1365,`)
- [ ] Minor: Copy: 15 fenced lines over 90 characters and 27 '---' rules (ratchet items) (`10-cli-reference.md:155, 158, 174, 655, 1319, 1443, 1446, 1638, 1639, 1662, 1665`)
- [ ] Minor: Dev: the cluster topology Section/Source table is a 27-row list that teaches nothing (`10-cli-reference.md:543-571`)
- [ ] Minor: Dev: limits before model in Resilience and Flow Pipelines (`10-cli-reference.md:1254, 2499-2501`)
- [ ] Minor: Dev: the Commands table is the page's best asset but its cells overflow and three families lack entries (`10-cli-reference.md:286-321; http://127.0.0.1:8765/10-cli-reference.html #comman`)
- [ ] Minor: Copy: mobile reading: 86 code blocks and 6 tables scroll sideways at 390 px (`http://127.0.0.1:8765/10-cli-reference.html at 390x844 (shots/ch-10-cli-referenc`)
- [ ] Minor: Fact: 'kates cluster alerts' Known-limitation text should also say the four critical rules make a healthy default install exit 1 (`10-cli-reference.md:602-608, 2855`)
- [ ] Polish: Copy: headings in sentence case and 'Workflow N:' headings (`10-cli-reference.md:42, 64, 82, 104, 125, 1674, 1687, 1712`)
- [ ] Polish: Copy: British spellings, missing unit spaces, 'the user', and a stray 'e.g.' habit (`10-cli-reference.md:207, 1058, 1283, 1436, 1639, 1644, 1702, 2605, 30, 52, 58, 5`)
- [ ] Polish: Copy: output blocks are introduced by 'Expected output:' and pin version numbers (`10-cli-reference.md:335, 364, 410, 477, 514, 964, 1072, 1522, 2064, 517-526`)
- [ ] Polish: Dev: the opening breaks the 'never open with This chapter ...' rule in spirit and promises a guide (`10-cli-reference.md:7-14`)
- [ ] Polish: Dev: 'Try it' callout on a Reference page, titled without its question (`10-cli-reference.md:2886-2899`)

### `16-grpc-api.md`

Scores: clarity 3, accuracy 4, structure 2, voice 3, completeness 3, examples 3.

**Critical — Fact: PERMISSION_DENIED is a Kates status the chapter says does not exist.** At `16-grpc-api.md:412-419, 489, 493`. The status table lists UNAUTHENTICATED (401/403) ... INTERNAL and the Summary says 'Kates raises six application status codes'. GrpcApiKeyInterceptor.java:98-105 closes a call whose principal lacks the method's scope with PERMISSION_DENIED ('The API key's principal <name> lacks the <scope> scope this method needs.'), and METHOD_SCOPES (lines 45-56) gives CreateTest and CancelTest test:run, DeleteTest admin, every other RPC read. 11-api-reference.md:67 already says 'over gRPC, PERMISSION_DENIED'.

Fix: Add the row `| PERMISSION_DENIED | 403 | The key's principal lacks the scope the RPC needs | The API key's principal ci-reader lacks the test:run scope this method needs. |`, change UNAUTHENTICATED's HTTP column to 401, and under Authentication add a three-row table 'RPC | Scope': CreateTest, CancelTest -> test:run; DeleteTest -> admin; GetTest, ListTests, every ClusterService RPC and HealthService/Check -> read, with one sentence linking to the REST chapter's scopes section. Change the Summary bullet to 'seven'.

The other findings, by severity:

- [ ] Major: Fact: the Authentication paragraph names one key setting; the interceptor resolves principals from the keys file too (`16-grpc-api.md:73`)
- [ ] Major: Dev: the page is built as an article, not as the Reference type STYLE.md prescribes (`16-grpc-api.md:3-31`)
- [ ] Major: Dev: no table maps REST status names to gRPC TestStatus values, yet the errors use the REST names (`16-grpc-api.md:242, 417`)
- [ ] Major: Dev: the sequence diagram contradicts the prose on when CreateTest returns, and its caption is a label (`16-grpc-api.md:35-59 (fig-grpc-test-execution-flow); shots/ch-16-grpc-api/fig-14`)
- [ ] Major: Copy: 16 fenced lines over 90 characters overflow the column on the site and the page in print (`16-grpc-api.md:76, 84, 193-195, 198-199, 221-222, 235, 274-276, 302, 343, 470; s`)
- [ ] Major: Dev: ListConsumerGroups has no entry (`16-grpc-api.md:258, 299-323`)
- [ ] Major: Dev: the 'never populated' callout is a seven-sentence wall that should be a table (`16-grpc-api.md:260-262; shots/ch-16-grpc-api/p0633.png`)
- [ ] Major: Dev: the proto3 JSON reading rules have no home section although the registry plans one (`16-grpc-api.md:149, 161, 207, 228, 281, 492; CONCEPTS.md:122`)
- [ ] Major: Web: the status-code table is unreadable at phone width (`http://127.0.0.1:8765/16-grpc-api.html #grpc-status-codes table; shots/ch-16-grp`)
- [ ] Major: Dev: response numbers read as captured output but are not labelled with their source (`16-grpc-api.md:133-147, 187-205, 218-226, 270-279, 305-321, 339-345`)
- [ ] Major: Dev: the Summary breaks every Summary rule and the Reference type has none (`16-grpc-api.md:486-494`)
- [ ] Minor: Fact: 'num_consumers ... Read by no test type' is wrong for the Trogdor backend (`16-grpc-api.md:383`)
- [ ] Minor: Fact: TestRun's scenario_name, labels and cdc_phases are populated but never described (`16-grpc-api.md:121, 389`)
- [ ] Minor: Fact: '~30% smaller (binary)' is an unsourced number (`16-grpc-api.md:30`)
- [ ] Minor: Dev: TestResult and TestSpec tables stop short of the proto, and TestRun has no table (`16-grpc-api.md:369-400`)
- [ ] Minor: Dev: Message Types, Error Handling and Equivalence are bare headings over bare blocks (`16-grpc-api.md:353-357, 391-393, 408-413, 435-437`)
- [ ] Minor: Copy: both callouts lack a title (`16-grpc-api.md:67, 260`)
- [ ] Minor: Copy: twelve horizontal rules (`16-grpc-api.md:18, 33, 61, 101, 246, 325, 351, 406, 433, 451, 477, 484`)
- [ ] Minor: Copy: no Glossary link in the running prose (`16-grpc-api.md:5, 7, 13, 73, 149, 250, 255, 387`)
- [ ] Minor: Copy: bold labels stand in for lead-ins and for 'Output:' (`16-grpc-api.md:7, 65, 131, 185, 216, 425`)
- [ ] Minor: Copy: marketing adjectives and buzz phrasing in the opening (`16-grpc-api.md:5, 7`)
- [ ] Minor: Copy: history words and 'current implementation' (`16-grpc-api.md:68, 121, 488, 491`)
- [ ] Minor: Copy: two sentences over 40 words (`16-grpc-api.md:99, 488`)
- [ ] Minor: Copy: terminology drift (Kates gRPC server, messages, target cluster, unified Quarkus server) (`16-grpc-api.md:13, 37, 44-45, 375-376, 494`)
- [ ] Minor: Print: the ClusterService RPC table overprints its first two columns (`PDF p.619 (physical 633); 16-grpc-api.md:252-258`)
- [ ] Minor: Dev: Pagination and the ignored status filter sit in the wrong places (`16-grpc-api.md:228, 402-404`)
- [ ] Minor: Dev: code generation writes into the repository root and omits the Python prerequisite (`16-grpc-api.md:455-471`)
- [ ] Minor: Dev: the chapter never says that CreateTest, CancelTest and DeleteTest leave an audit row (`16-grpc-api.md:107-118`)
- [ ] Polish: Copy: UK spelling and Latin abbreviation (`16-grpc-api.md:176, 423`)
- [ ] Polish: Dev: 'See Also' duplicates the handoff and the intro links (`16-grpc-api.md:479-482`)
- [ ] Polish: Copy: heading wording and the slash heading (`16-grpc-api.md:3, 20, 230, 435`)

### `appendix-compliance-language.md`

Scores: clarity 4, accuracy 4, structure 4, voice 5, completeness 4, examples 3.

The other findings, by severity:

- [ ] Major: Fact: Placement says any other zoneLabel is UNKNOWN, omitting rbac.nodeReads (`appendix-compliance-language.md:174 (also :207)`)
- [ ] Major: Dev: the Placement paragraph is nine conditional rules in one block; make it a table (`appendix-compliance-language.md:174`)
- [ ] Major: Web: 12 of 20 tables clip their third column at phone width (`http://127.0.0.1:8765/appendix-compliance-language.html main table (screenshots `)
- [ ] Minor: Dev: statuses are used from the first section but never keyed on the page (`appendix-compliance-language.md:9 (first use), :314 (definition)`)
- [ ] Minor: Dev: the opening promises editor, CLI and API agree "alike", which the page itself qualifies (`appendix-compliance-language.md:3`)
- [ ] Minor: Dev: no captured report example anchors the two result tables (`appendix-compliance-language.md:292-326`)
- [ ] Minor: Dev: "Rules No Cluster Can Meet" names a section that is mostly warnings a cluster can meet (`appendix-compliance-language.md:364-366`)
- [ ] Minor: Dev: Broker Settings' `brokers` paragraph is six UNKNOWN/FAIL conditions in prose (`appendix-compliance-language.md:242`)
- [ ] Minor: Dev: JUnit is named once and the page never says what produces it (`appendix-compliance-language.md:81`)
- [ ] Minor: Dev: The Report omits that the report is stored and readable back (`appendix-compliance-language.md:294`)
- [ ] Minor: Dev: acks=all and unclean leader election are re-explained instead of glossed (`appendix-compliance-language.md:393`)
- [ ] Minor: Dev: PodMonitor and EndpointSlice have no Glossary entry and are used unglossed (`appendix-compliance-language.md:118, 174, 265-269`)
- [ ] Minor: Copy: Glossary links missing at first use (KRaft, ISR, broker) (`appendix-compliance-language.md:180, 375, 49`)
- [ ] Polish: Copy: US spelling "labelled" (`appendix-compliance-language.md:120`)
- [ ] Polish: Copy: "lower-case" hyphenated (`appendix-compliance-language.md:21, 225, 233`)
- [ ] Polish: Copy: "id" for identifier, in the heading and ten times in prose (`appendix-compliance-language.md:79 (## Rule Ids), 11, 81, 92, 103, 126, 138, 196`)
- [ ] Polish: Copy: "Kafka CR" once against "`Kafka` resource" everywhere else (`appendix-compliance-language.md:174`)
- [ ] Polish: Copy: link text not the heading verbatim (`appendix-compliance-language.md:207`)
- [ ] Polish: Copy: ellipsis glued to a code span (`appendix-compliance-language.md:238`)
- [ ] Polish: Dev: the Value Kinds rules list is the page's one list-heavy section (`appendix-compliance-language.md:66-73`)
- [ ] Polish: Dev: the listener-severity rule is stated three times (`appendix-compliance-language.md:77, 92, 211`)
- [ ] Polish: Dev: Size Limits doesn't say the API answers 413 over 256 KiB (`appendix-compliance-language.md:439-443`)
- [ ] Polish: Dev: the page ends on a table with no closing line (`appendix-compliance-language.md:456-466`)
- [ ] Polish: Dev: the two gotchas writers hit are buried in a bullet and a paragraph (`appendix-compliance-language.md:70, 240`)
- [ ] Polish: Fact: "Strimzi refuses a listener port below 9092" is sourced only from lint (`appendix-compliance-language.md:227`)
- [ ] Polish: Print: inline code in the appendix's tables prints far smaller than the body (`PDF p.706-707 (shots/ch-appendix-compliance-language/pdf/p0706.png), also p.695`)
- [ ] Polish: Copy: "the prose beside the table" misdescribes the layout (`appendix-compliance-language.md:9`)

## Not Covered

These reviews were cut off before they reported, so their pages are covered only by the print and site work above: the Preface and the six Part pages, the REST API Reference, the Glossary, the Troubleshooting Index, the CI/CD Pipeline and Version Matrix appendices, and the cross-cutting reviews of voice, pedagogy, figures, code samples, citations, cross-references, accessibility and site performance. They are the next review round.
