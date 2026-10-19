# `kates dashboard`: concise and insight-first (plan, 2026-10-18)

Status: plan only, uncommitted. Decisions D1–D7 taken 2026-10-18 (§11): the recommended pick each time. Wave 4 and the backend changes (§10) are deferred until the new layout has been used. Line references are against `origin/main` 10aabe4. That commit has all of the perf plan's Wave 1, including #389 and #393. The local branch `fix/fix_issue_fot_the_same_bug` is behind it, and its `dashboard.go` lines run 3 lower.

Scope: `kates dashboard` (alias `dash`) in `cli/cmd/dashboard.go`, the one file behind it. `cli/tui` is not the dashboard: it holds `kates kafka tui` (the Brokers, Topics, Groups and Produce tabs) and `kates lab`. The plan uses `kates kafka tui` only as a drill-down link. Its bugs are in `plans/cli-performance-stability.md` (F8, item 6.2), and this plan does not repeat them.

## TL;DR

- **The dashboard reports a CRITICAL cluster as healthy.** With 2 partitions offline it shows `Kafka ● UP` and `ISR Health ✓ 0 under-replicated`. It reads only the under-replicated count from `/api/cluster/check` and ignores the check's own `status`, its `offline` count and the KRaft quorum (§2, U1).
- **The frame does not fit any terminal.** Every row of panels is 2 columns wider than the screen at every width, so every line wraps. A frame is 66 rows at 80 columns and 56 at 120. At 80×24 you see only Cluster Detail and Quick Commands; at 120×40 the health and summary panels have scrolled off (U2, U3).
- **It blinks and lies by omission.** Each refresh clears the screen, then fetches. Against a stub answering in 300 ms, 21 of 90 samples were blank and 9 were half drawn. When the API is down the counts read 0, and a STOPPING run is listed as completed with a ✓ (U4–U7).
- **Most of what it shows answers no question anyone acts on.** That covers the bootstrap address (labelled "Brokers"), "10 test configs loaded", the all-time run total, the controller id, and four static Quick Commands, three of which start load. The throughput and p99 panels never render in practice, because list items carry no results (perf F27). When they do render, they sum unrelated runs (U8, U10).
- **Target:** one verdict line, then at most 3 "needs attention" lines, each with the command to run next. Then three one-line status rows (Kafka, Runs, Chaos), a Running table showing progress against plan, target and SLA, and a "Latest by type" board showing a pass/fail history and the delta against the baseline. It fits 80×24 with no borders and no blank frames (§7).
- **Waves:** 1 PR of fixes now, 2 for the foundation (a pure render plus a Bubble Tea shell), 2 for the new layout, 4 small reads for added insight (deferred), and 1 for docs. Each wave is small and stands alone. No backend change is required; 5 optional backend changes are listed separately (§10), deferred too.

## 1. What it is today

A print loop (`dashboard.go:20-266`). Every `--interval` seconds (3 by default) it does the following:

1. Clears the screen (`:29`).
2. GETs `/api/health` and `/api/tests?page=0&size=50`, one after the other (`:31-32`). The list error is discarded (`paged, _ :=`).
3. Prints four rows of two rounded panels:
   - System Health and Test Summary.
   - Active Tests and Recent Completed.
   - Throughput and P99, only once a history exists.
   - Cluster Detail and Quick Commands. Between the third row and this one it GETs `/api/cluster/check` (`:221`).
4. Sleeps.

It has no tests (only `interval_test.go` touches it), no keys, no alt screen and no resize handling. It also ignores the house 80×24 floor in `cli/tui/toosmall.go`.

How the evidence was gathered:

- Ran `main`'s CLI, built in the session scratchpad, inside tmux at 80×24, 120×40, 121×40, 160×50 and 200×50.
- Pointed it at a Python stub API whose shapes come from `HealthResource`, `ClusterHealthService.clusterHealthCheck` and `EntityMapper.toDomainSummary`. The stub ran in five states: healthy, degraded (2 offline partitions, cluster CRITICAL), empty, with results (as if F27 were fixed), and down (`127.0.0.1:9`).
- `HOME` and `KATES_URL` pointed at scratch. Nothing touched the real API on `localhost:8080`.

## 2. Critique

### What an 80×24 terminal shows (healthy cluster, 3 active runs)

The whole visible screen, as captured:

```
                                          │ 2026-10-18 16:08:26
 │
                                          │
 │
                                          ╰─────────────────────────────────────
─╯

╭──────────────────────────────────────╮  ╭─────────────────────────────────────
─╮
│ Cluster Detail                       │  │ Quick Commands
 │
│   Brokers 3                          │  │   ▸  kates test create --type LOAD
 │
│   Topics 42                          │  │   ▸  kates benchmark
 │
│   ISR Health ✓ 0 under-replicated    │  │   ▸  kates gate --min-grade B
 │
│   Controller broker-3                │  │   ▸  kates test cleanup
 │
╰──────────────────────────────────────╯  ╰─────────────────────────────────────
─╯

  ⠙ Refreshing every 3s... (Ctrl+C to stop)
```

Nothing about the runs is visible, and nothing says whether the API or Kafka is up.

### What a 121×40 terminal shows at the top of the frame (scrolled off at 40 rows)

```
   Kates Dashboard  │  20:18:59

╭──────────────────────────────────────────────────────────╮  ╭──────────────────────────────────────────────────────────
╮
│ System Health                                            │  │ Test Summary
│
│   API ● UP                                               │  │     2 Running       1 Pending
│
│   Kafka ● UP                                             │  │     4 Done        2 Failed
│
│   Brokers krafter-kafka-bootstrap.kafka.svc:9092         │  │   ····················································
│
│   Engine native                                          │  │   214 Total tests
│
│   Configs 10 test configs loaded                         │  ╰──────────────────────────────────────────────────────────
╯
╰──────────────────────────────────────────────────────────╯
```

### The cluster is CRITICAL (2 offline partitions on `orders`), 160×50, left column

```
│ System Health                                                                │
│   API ● UP                                                                   │
│   Kafka ● UP                                                                 │
...
│ Cluster Detail                                                               │
│   Brokers 3                                                                  │
│   Topics 42                                                                  │
│   ISR Health ✓ 0 under-replicated                                            │
│   Controller broker-3                                                        │
```

### API down (`KATES_URL=http://127.0.0.1:9`), 160×50, wrapped borders removed

```
│ System Health                │  │ Test Summary                 │
│   ✖ API unreachable          │  │     0 Running       0 Pending│
                                  │     0 Done        0 Failed   │
                                  │     0 Total tests            │
│ Active Tests                 │  │ Recent Completed             │
│   No active tests            │  │   No completed tests         │
```

### Findings

The tags work as follows: VERIFIED means seen in a capture or measured, and READ means taken from the source.

| # | Finding | Evidence |
| --- | --- | --- |
| U1 | **A CRITICAL cluster is shown as healthy.** The ISR row checks only `underReplicated` (`:229-236`). Offline partitions are counted separately by the backend (`ClusterHealthService.java:311-318`), and those, or a KRaft quorum with no leader, set `status: CRITICAL`, which the dashboard never reads. `Kafka ● UP` comes from `/api/health`, which only asks whether `describeCluster` answers. | VERIFIED (degraded capture) |
| U2 | **Every panel row is wider than the terminal at every width.** `panelW := w/2 - 2` (`:35`), but `output.Panel` adds a 2-column border, so a row is `2·(w/2-2+2)+2 = w+2` wide. Every line wraps, and the right border lands on a line of its own. | VERIFIED at 80, 120, 121, 200 |
| U3 | **A frame is taller than the screen.** It is 66 rows at 80 columns, 56 at 120 and 54 at 200. At 80×24 only the last panel row and the footer are visible; at 120×40 the health and summary panels are off screen. | VERIFIED |
| U4 | **Blank and half-drawn frames on every refresh.** It clears first (`:29`), then makes 2 sequential GETs, prints 3 rows, makes a third GET (`:221`), and prints the rest. Sampled every 100 ms against a stub with 300 ms latency: 21 of 90 samples blank, 9 partial. Against a refused port each frame took about 4 s to fill (retries, perf F3). `\033[2J` also pushes each old frame into scrollback (113 scrollback lines after 2 refreshes in tmux). | VERIFIED |
| U5 | **Unknown is shown as zero.** With the API down, Test Summary reads `0 Running … 0 Total tests`, because the list error is dropped (`:32`). | VERIFIED |
| U6 | **A STOPPING run is "Recent Completed" with a green ✓.** Anything that is not RUNNING or PENDING is "completed" (`:164`), and anything that is not FAILED or ERROR gets ✓ (`:172`). | VERIFIED |
| U7 | **The counts mix scopes.** Running, Pending, Done and Failed count the latest 50 runs, while "Total tests" is all time (214). STOPPING is in no bucket: 2+1+4+2 = 9 of the 10 listed. Nothing says what window the numbers cover. | VERIFIED |
| U8 | **The metric panels are empty in practice, and misleading when full.** List items carry no `results` (perf F27, `EntityMapper.toDomainSummary`), so Active Tests shows no numbers and the Throughput and P99 panels never appear. With results stubbed in: Throughput sums unrelated runs (`:131`, 179.6K = LOAD + STRESS); "P99 Latency" is the p99 of whichever running run came last (`:132`); sparkline colors are relative to the window's own min and max, so red means "highest seen" and not "bad" (`output.go:384-433`); the ↗ and ↘ in the titles are decoration; and a flat series draws as `▁▁▁▁`. | VERIFIED (results stub) |
| U9 | **Labels are misaligned in a terminal but aligned in a pipe.** `%-10s` and `%-16s` pad strings that are already styled, and the ANSI bytes count toward the width (`:58-75`, `:223-237`, `:124`, `:175`). So a TTY shows `Brokers 3` and `Controller broker-3` ragged, while piped output lines up. | VERIFIED (TTY vs pipe) |
| U10 | **Static or mislabelled fields.** "Brokers" is the bootstrap address (`:66`) while the other "Brokers" is a count. Then there is "Configs 10 test configs loaded" (`:75`), "Engine native", and "Controller broker-3" with no quorum health. The Quick Commands (`:244-247`) never change with state: `test create`, `benchmark` and `gate` start load, and `test cleanup` deletes runs. | READ |
| U11 | **No identity.** Nothing says which context, API URL or Kafka cluster you are looking at, though `kates status` prints the context and URL. | READ |
| U12 | **Color is decoration.** Throughput is always green, latency always amber and Pending amber, whatever the values. | READ |
| U13 | **Freshness is unknown.** `/api/cluster/check` is cached for 30 s on the server (`ClusterHealthService.java:45,236`) and carries no timestamp, yet the frame shows a new clock every 3 s. A failed refresh replaces good data with an error instead of keeping it and marking it stale. | READ |
| U14 | **No key hints, no size floor.** Only "Ctrl+C to stop". No too-small screen, unlike the house TUIs (`tui/toosmall.go`, 80×24). | READ |
| U15 | **The docs promise more.** The book calls it "the CLI equivalent of the Grafana Benchmark dashboard" with a "Recent test results table" (`docs/book/09-observability.md:347-358`). `10-cli-reference.md:1162-1172` and `tldr.go:75` need the same update. | READ |

## 3. Every panel, field and number

The "also in" column lists where else the same answer exists: `kates status`, `kates top`, `kates cluster watch`, `kates test watch`, `kates health` and the Grafana boards under `dashboards/`.

| Panel · field | Question it answers | Does anyone act on it? | Also in | Verdict |
| --- | --- | --- | --- | --- |
| Title · clock | Is it live? | No | every live view | **Replace**: context · cluster id · "updated Ns ago" |
| Health · API ● UP | Can I reach Kates? | Yes (`doctor`) | status, top, health | **Merge** into the verdict (A1) |
| Health · Kafka ● UP | Can Kates reach Kafka? | Yes | status, top, health | **Merge** into the verdict (A2) |
| Health · "Brokers" bootstrap address | Where is Kafka? | No, static | health | **Cut** |
| Health · Engine | Which backend runs tests? | Rarely | health, `test backends` | **Cut**; at 120 columns it shows per run |
| Health · Configs N loaded | None | No | health, `test types` | **Cut** |
| Summary · Running / Pending / Done / Failed | How busy, is anything failing? | Partly | top, status | **Merge** into the Runs line, with a stated window |
| Summary · Total tests (all time) | None | No | `test list` | **Cut** |
| Active · type, id | What is running? | Yes (`test watch`) | top | **Keep**, as the Running table |
| Active · records, rate, p99 (blank today) | Is it performing? | Yes | top (blank too), test watch | **Keep**, against target and SLA (W4.1) |
| Recent Completed · 5 rows | What just finished? | Partly | top (10 rows), `test list` | **Merge** into Latest by type, with a history strip |
| Throughput panel (sum of runs) | None | No | test watch per run; `kates-trend` board | **Cut** |
| P99 panel (last run's p99) | None | No | test watch; `kates-trend` | **Cut** |
| Cluster · Brokers N | Are the brokers up? | Only if "expected" is known | cluster watch | **Keep**, as N/expected (W4.4) |
| Cluster · Topics N | Size of the cluster | No | cluster watch, info | **Cut** at 80 columns, keep at 120 |
| Cluster · ISR (URP only) | Is data at risk? | Yes | cluster watch, `kafka-kraft` board | **Fix and keep**: offline, URP, quorum (A3, A5) |
| Cluster · Controller broker-N | None | Rarely | cluster watch, topology | **Replace** with quorum health |
| Quick Commands (static) | None | No | help, tldr | **Cut**; replaced by context-aware next commands |
| Footer spinner, "Ctrl+C" | Is it live? How do I quit? | No | top, cluster watch | **Replace** with data age and keys |

**Grafana:** `kates-overview`, `kates-trend`, `kafka-kraft` and `kafka-performance` already chart time series in depth: per-run p99 and throughput trends, quorum, offline partitions, ISR churn and request latency. The terminal dashboard should not chart time series. Its job is the verdict and the next step. The only history it keeps is the pass/fail strip per test type, which no board shows at a glance.

## 4. Missing insight, and what each piece costs

| Insight | Source (already in the API) | Cost per refresh |
| --- | --- | --- |
| One verdict: all clear, N to check, or N need attention | Rules A1–A9 below | 0 extra |
| Cluster verdict with the offending partitions | `/api/cluster/check` `status`, `partitionHealth.offline`, `.problems`, `kraftQuorum` (fetched today, ignored) | 0 |
| Brokers up against expected | `check.brokers` against the sum of `nodePools[].replicas` with a broker role (`/api/cluster/topology`) | 1 heavy GET at start, then every 5 min |
| Run progress | `createdAt` + `plannedDurationMs`: in every list item, but missing from the CLI's `client.TestRun` | 0 (add the field) |
| Rate against target, p99 against SLA, live | `GetTest(id)` for running rows (`results`); `spec.throughput` / `targetThroughput`; `sla` (in the summary, missing from `client.TestRun`) | 1 GET per running row, capped at 5 |
| Latest result per test type, and a pass/fail history | The 100-run page `kates status` already reads (sorted newest first, `TestRunRepository.java:129`) | 0 |
| Why the latest failure failed | `GetTest(id).results[].error` | 1 GET per new failed run, cached |
| Delta against baseline | `/api/tests/baselines` + `/api/tests/{id}/report/regression` for the latest DONE run per baselined type | Cached per run id: only when a new run finishes |
| Chaos context ("URP during broker-kill" is expected) | `/api/disruptions?page=0&size=5`: `status` RUNNING, `planName`, `slaGrade` | 1 GET |
| Freshness | Time of the last good answer (client side) | 0 |

**Not cheap today (backend, optional, §10):** the age of the cluster check, firing alerts (`/api/cluster/alerts` lists configured PrometheusRules, not firing ones, `ClusterAlertsService`), consumer lag per group, and run counts by window.

## 5. Cut, merge, keep, add

- **Cut** the bootstrap address, Engine, Configs, Total tests, both sparkline panels, Topics (at 80 columns), the controller id, the static Quick Commands, the dotted rule, the title background and the spinner. Each answers no question someone acts on, and each is still in `kates health`, `kates cluster info` or Grafana for anyone who wants it.
- **Merge** API, Kafka and the cluster check into one verdict plus the attention lines. Merge the four counts into one Runs line with a stated window. Merge Recent Completed into Latest by type, because the latest result per type is the state worth acting on, not the last 5 rows.
- **Keep** the running runs (now with progress, target and SLA), the partition health (now complete) and the brokers (now against expected).
- **Add:**
  - The verdict line and up to 3 attention lines, each with its next command.
  - Identity (context and cluster id) and freshness.
  - The Chaos line.
  - The history strip and baseline delta per type.
  - The key hints `q` and `r`.

## 6. Information hierarchy

| Level | Seen in | Content |
| --- | --- | --- |
| L0 | Under a second, row 1 | Verdict glyph and words (`✓ All clear` / `⚠ N to check` / `✖ N need attention` / `○ Stale` / `✖ Kates API unreachable`); context · cluster · data age |
| L1 | A few seconds, rows 3–5 | Attention lines, worst first, at most 3 at 80×24 and 5 at 120×40, each `glyph · subject · what is wrong, in words and numbers · next command` |
| L2 | Status rows | `Kafka` (brokers, quorum, partitions), `Runs` (running, pending, stopping, finished in 24 h, failing types), `Chaos` |
| L3 | Running table | Type, id, elapsed of planned, rate, % of target, p99, SLA state; capped by height, then `+N more · kates top` |
| L4 | Latest by type | Last result and its age, history (older → newer), delta against baseline |
| Drill-down | Commands, never inline | `kates test watch <id>`, `kates test get <id>`, `kates cluster watch`, `kates report regression <id>`, `kates disruption watch <id>`, `kates top`, `kates kafka tui`, `kates doctor` |

### Attention rules

Glyphs come from `output.Glyphs()`: ✖ is Cross and ⚠ is Warn, with ASCII fallbacks `x` and `!`. Items are ordered by severity, then by rule number.

| # | Sev | Fires when | Source | Next command |
| --- | --- | --- | --- | --- |
| A1 | ✖ | Any GET to the Kates API fails: refused, timeout, 401/403 | client error class | `kates doctor` |
| A2 | ✖ | Kafka is unreachable from the API | `health.kafka.status == DOWN` | `kates cluster check` |
| A3 | ✖ | Offline partitions, or a KRaft quorum with no leader | `check.status == CRITICAL`, `offline`, `kraftQuorum.hasLeader` | `kates cluster watch` |
| A4 | ✖ | Fewer brokers than the node pools expect (W4.4) | `check.brokers` against node pool replicas | `kates cluster topology` |
| A5 | ⚠ | Under-replicated partitions | `partitionHealth.underReplicated` | `kates cluster watch` |
| A6 | ✖ | The latest finished run of a test type FAILED | list page | `kates test get <id>` |
| A7 | ⚠ | A running run breaks its SLA live: p99 over `sla.maxP99LatencyMs`, or a rate under `sla.minThroughputRecPerSec` after its first minute | `GetTest` + `sla` (W4.1) | `kates test watch <id>` |
| A8 | ⚠ | A running run is under 90% of its target rate for 2 polls after its first minute | `GetTest` + spec target (W4.1) | `kates test watch <id>` |
| A9 | ⚠ | The latest DONE run of a baselined type regressed | `regressionDetected` (W4.3) | `kates report regression <id>` |

How the rules combine:

- **During chaos:** while a disruption is RUNNING, A3–A5 drop to ⚠ and say "during chaos". A broken cluster is then expected, but it is still shown.
- **Unknown inputs:** when A1 fires, everything that depends on the API shows `—`, never 0.
- **Ages are informational:** PENDING and STOPPING ages show in the table but never raise attention. A PENDING run may be waiting for its phase start, and the reaper (#354) fails runs past their deadline.

## 7. Target layout

The layout follows these rules:

- **No borders.** Sections are separated by blank lines and an upper-case header, aligned on fixed columns.
- **Plain text, then style.** Padding is done on plain text first and styled afterwards; truncation uses `…`, measured with `lipgloss.Width`.
- **Numbers:** right-aligned, using the house `fmtNum` (`48.2K`, `1.2M`) with a unit on every number.
- **Ages:** relative (`47m`, `2h`) instead of 19-character timestamps.
- **Color** is only on the state glyph and its word, plus on a value that breaks its threshold. Everything else uses the default or dim foreground.
- **Height:** every frame fits 80×24. Below that, the house `TooSmallCard` shows. Sections give up rows bottom-up: Latest by type first, then Running rows beyond 1. The verdict and attention lines never give up rows.

The ids, numbers and failure counts below are illustrative. The mockups show the full target, Wave 4 included; while Wave 4 is deferred, its columns and lines are left out (see Wave 3).

### 80×24, healthy (18 rows)

```
✓ All clear                                      lab · Xk3pQ9rL · updated 2s ago

Kafka    3/3 brokers · quorum ok, leader 3 · 386 partitions in sync
Runs     2 running · 1 pending · 9 finished in 24 h, none failing
Chaos    none in 24 h

RUNNING                       ELAPSED       RATE   TARGET       P99   SLA
LOAD       3f9a1c2e         4m of 10m    48.2K/s      96%   41.7 ms     ✓
STRESS     b71e0d94         9m of 15m   131.4K/s        —    212 ms     —
SPIKE      0c55a7f1       pending 12s

LATEST BY TYPE  LAST     OLDER → NEWER       VS BASELINE
LOAD            ✓ 47m    ✓ ✓ ✓ ✓ ✓ ✓         p99 +4%, rate -1%
INTEGRITY       ✓ 1h     ✓ ✓                 no baseline
STRESS          ✓ 2h     ✓ ✓ ✓               no baseline
VOLUME          ✓ 3h     ✓                   no baseline

kates top · kates test watch 3f9a1c2e                          q quit  r refresh
```

`—` means "not set": STRESS here has no target rate and no SLA.

### 80×24, needs attention (22 rows)

```
✖ 3 need attention                               lab · Xk3pQ9rL · updated 1s ago

✖ Kafka      2 partitions offline: orders-4, orders-7  kates cluster watch
✖ INTEGRITY  latest run failed 1h ago                  kates test get 5e4f3a2b
⚠ STRESS     p99 212 ms, SLA 150 ms                    kates test watch b71e0d94

Kafka    3/3 brokers · quorum ok, leader 3 · 386 partitions, 2 offline
Runs     2 running · 1 pending · 9 finished in 24 h, 1 type failing
Chaos    none in 24 h

RUNNING                       ELAPSED       RATE   TARGET       P99   SLA
LOAD       3f9a1c2e         4m of 10m    48.2K/s      96%   41.7 ms     ✓
STRESS     b71e0d94         9m of 15m   131.4K/s        —    212 ms     ⚠
SPIKE      0c55a7f1       pending 12s

LATEST BY TYPE  LAST     OLDER → NEWER       VS BASELINE
LOAD            ✓ 47m    ✓ ✓ ✓ ✖ ✓ ✓         p99 +4%, rate -1%
INTEGRITY       ✖ 1h     ✓ ✖                 no baseline
STRESS          ✓ 2h     ✖ ✓ ✓               no baseline
VOLUME          ✓ 3h     ✓                   no baseline

kates cluster watch · kates top                                q quit  r refresh
```

STRESS failed once, but its latest run passed, so it shows in the history strip and not as an attention line (A6).

### 80×24, other states

```
✖ Kates API unreachable                                      lab · no answer yet

  http://127.0.0.1:8080: connection refused

  Kafka, runs and chaos are unknown until the API answers.

kates doctor · kates ctx current               retrying in 3s  q quit  r refresh
```

When a refresh fails after good data, the verdict line changes and the last frame stays on screen, dimmed:

```
○ Stale 24s: the Kates API timed out        lab · Xk3pQ9rL · last answer 24s ago
  The rows below are the last answer, not the current state.
```

```
✓ All clear · no runs yet                        lab · Xk3pQ9rL · updated 1s ago

Kafka    3/3 brokers · quorum ok, leader 3 · 386 partitions in sync
Runs     none yet
Chaos    none in 24 h

kates test create --type LOAD · kates test types               q quit  r refresh
```

```
⚠ 2 to check                                     lab · Xk3pQ9rL · updated 1s ago

⚠ Kafka      1 of 3 brokers down, during chaos    kates cluster topology
⚠ Kafka      4 under-replicated, during chaos     kates cluster watch

Kafka    2/3 brokers · quorum ok, leader 1 · 386 partitions, 4 under-replicated
Runs     1 running · 6 finished in 24 h, none failing
Chaos    broker-kill running for 1m (d41c9e07)
```

The first frame prints at once as `… connecting to <url>`, so the screen is never blank while the first GET is in flight.

### 120×40, needs attention (23 rows; more columns, not more boxes)

```
✖ 3 need attention                                                       context lab · cluster Xk3pQ9rL · updated 1s ago

✖ Kafka      2 partitions offline, both on topic orders: orders-4, orders-7                    kates cluster watch
✖ INTEGRITY  the latest INTEGRITY run failed 1h ago; the 2 before it passed                    kates test get 5e4f3a2b
⚠ STRESS     p99 212 ms is over its SLA of 150 ms, 9m into a 15m run                           kates test watch b71e0d94

Kafka    3/3 brokers · KRaft quorum ok, leader 3 of 3 voters · 42 topics · 386 partitions, 2 offline · 12 groups
Runs     2 running · 1 pending · 1 stopping · 9 finished in 24 h, 1 type failing
Chaos    none in 24 h

RUNNING                                      ELAPSED   RECORDS       RATE   TARGET       P99   SLA   BACKEND
LOAD        3f9a1c2e  checkout-peak        4m of 10m    412.0K    48.2K/s      96%   41.7 ms     ✓   native
STRESS      b71e0d94                       9m of 15m    412.0K   131.4K/s        —    212 ms     ⚠   native
ENDURANCE   e2d4f6a8                    stopping 31m                                                 native
SPIKE       0c55a7f1                     pending 12s                                                 native

LATEST BY TYPE  LAST     RUN       OLDER → NEWER (10)        VS BASELINE
LOAD            ✓ 47m    9a8b7c6d  ✓ ✓ ✓ ✓ ✓ ✓ ✓ ✖ ✓ ✓       p99 +4%, rate -1% (base 1d2c3b4a)
INTEGRITY       ✖ 1h     5e4f3a2b  ✓ ✓ ✖                     no baseline
STRESS          ✓ 2h     7f6e5d4c  ✓ ✓ ✖ ✓                   no baseline
VOLUME          ✓ 3h     aa11bb22  ✓                         no baseline

kates cluster watch · kates test get 5e4f3a2b · kates top · kates kafka tui                            q quit  r refresh
```

From 160 columns up, the same layout grows a LAST FAILURE column, filled from the first `results[].error` of each type's latest failed run. Nothing else changes.

### Readability rules the implementation must hold

These are checked by tests, not by eye.

- Every line is at most `width` by `lipgloss.Width`, and the frame has at most `height` rows, at 80×24, 100×30, 120×40 and 200×50.
- Columns are padded on plain text before styling, so a TTY and a pipe align the same (U9).
- Every state carries a glyph and a word, so it reads the same with `NO_COLOR`, with `KATES_ASCII=1`, and on light or dark themes. The palette is already `AdaptiveColor` through `cli/pkg/theme`. No hex literals and no raw escapes, as `scripts/check-cli-style.sh` requires.
- A value never shows as `0` when it is unknown; it shows as `—`.
- The data age is always on row 1. After a failed refresh the last good data stays, and the verdict becomes `○ Stale`.
- Each refresh repaints in place: no blank frame and no scrollback growth.

## 8. Coordination with `plans/cli-performance-stability.md`

This plan repeats none of the perf items. Where both plans touch `dashboard.go`, the overlap is resolved like this:

- **F28 / 1.6 (`--interval` under 1 s)** merged as #393, and `checkInterval` stays.
- **F3 / 2.1 (probe timeouts)** lists `dashboard` among its probes. W2.1 here gives each GET the same 5 s context. Whichever lands first owns the dashboard line, and the other drops it.
- **F27 / 6.7 (GetTest for RUNNING rows)** lists `top.go` and `dashboard.go`. W4.1 here is the dashboard half, and **6.7 should drop `dashboard.go`** from its file list so the same code is not written twice.
- **6.2 (pollers re-arm after the result)**: W2.2 follows that pattern from the start, with one fetch in flight.
- **F8 / 1.3 (#389) and 6.2 (`kafka tui` tabs)** are out of scope here.

## 9. Not in scope, found in passing

These are not part of the dashboard work; they are noted so they don't get lost.

- `kates cluster watch` has the same clear-then-fetch blink, and polls a check that is cached for 30 s every 5 s. Its "Trend (last 30 polls)" therefore repeats each real sample about 6 times. The W2.2 shell would fix it with no new design.
- `kates top` has the same blink (`top.go:25-28`).
- `client.DisruptionList` sends `?limit=`, but `GET /api/disruptions` reads only `page` and `size` (`DisruptionResource.java:131-133`), so it always gets 50. W4.2 uses `page`/`size` (as `MCPActivityDisruptions` already does) and leaves `DisruptionList` alone.
- `kates cluster alerts` says "Show critical Kafka cluster alerts", but the endpoint lists configured rules, not firing ones.

## 10. PR waves

Size key: XS is up to 30 changed lines of non-test code, S up to 150, M up to 400. Every PR runs `go vet`, `go test ./...`, `-race` on `cli/cmd`, `golangci-lint --new-from-rev=origin/main`, `scripts/check-cli-style.sh` and `scripts/check-cli-compat.sh`. Every render test uses the plain color profile and fixed `now`, width and height.

### Wave 1: stop the dashboard lying (current layout)

| PR | Change | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 1.1 | The cluster row reads `check.status`. It shows offline partitions and a missing KRaft leader as ✖, and URP as ⚠. STOPPING counts as active, never "completed ✓". When a GET fails its counts show `—`, not 0, and the list error is no longer dropped. | `cli/cmd/dashboard.go`, new `cli/cmd/dashboard_test.go` | Extract the three row builders as functions. Table test: a CRITICAL check with 2 offline renders ✖ and "2 offline"; a STOPPING run never renders ✓; a failed list renders `—` | S |

The wrap and alignment fixes (U2, U9) are left out on purpose, because Wave 3 deletes the panels (D4).

### Wave 2: foundation (same content, no blink)

| PR | Change | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 2.1 | Split fetch from render. `fetchDashboard(ctx) dashSnapshot` runs the 3 GETs in parallel through an errgroup, as `status.go` does, each with a 5 s context, and keeps per-source errors. `renderDashboard(s, w, h, now) string` is pure. The print loop calls both. This also fixes U2, U3 and U9, because the render owns the widths. | `dashboard.go`, `dashboard_test.go` | Render at 80×24 and 120×40: every line within the width, row count within the height. An `httptest` stub counting GETs: exactly 1 per endpoint per refresh, in parallel (a 300 ms stub refreshes in under 400 ms) | M |
| 2.2 | Bubble Tea shell (D1). `tea.WithAltScreen()`; a tick starts a fetch only when none is in flight; the last good snapshot is kept and marked stale on error; `q` quits and `r` refreshes; `tui.TooSmallCard` below 80×24; resize is redrawn at once. A non-TTY keeps today's append-only frames, built from the same render. | `dashboard.go`, `dashboard_test.go` | Model tests: an error after data keeps the data and shows `○ Stale`; a second tick while a fetch is in flight starts none; `r` refreshes; `q` quits. Non-TTY: output has no escape bytes | S |

### Wave 3: insight-first layout from data already fetched

While Wave 4 is deferred, Wave 3 shows only what it can fill. 3.1 leaves out the Chaos line rather than reading "unknown", and 3.2 leaves out the RATE, TARGET, P99 and SLA columns and VS BASELINE. Each comes back with its Wave 4 PR.

| PR | Change | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 3.1 | The verdict line, attention rules A1, A2, A3, A5 and A6 with next commands, and the Kafka and Runs status lines (the Chaos line comes with 4.2). Cut every panel §5 lists. The list size goes from 50 to 100, the same as `kates status`. The window is "in 24 h", labelled "100+" when the page does not reach 24 h back (D6). | `dashboard.go`, `dashboard_test.go`, new `dashboard_rules.go` holding the rules and thresholds | Rule table tests: one case per rule, plus ordering, and the 3-line cap with "+N more" (the during-chaos downgrade is tested in 4.2) | M |
| 3.2 | The Running table: elapsed of planned (`client.TestRun` gains `PlannedDurationMs *int64`), PENDING and STOPPING ages, capped with `+N more · kates top`. Latest by type with its history strip, newest last. | `dashboard.go`, `client/types.go`, tests | Rows given up by height at 80×24 / 80×30 / 120×40; a run with no planned duration shows its age only | S |

### Wave 4: a few more reads, each optional and independent (deferred)

Deferred 2026-10-18 until the Wave 3 layout has been used. Open one of these only when its missing piece is felt in use; each stays independent of the others.

| PR | Change | Files | Tests | Size |
| --- | --- | --- | --- | --- |
| 4.1 | Live metrics for running rows: `GetTest` per row, at most 5, in parallel, giving rate, p99, % of target, and SLA ✓/⚠ (`client.TestRun` gains `Sla`). Rules A7 and A8. This is the dashboard half of perf 6.7. | `dashboard.go`, `dashboard_rules.go`, `client/types.go`, tests | A stub with `results` and `sla`: the SLA breach raises ⚠ with `kates test watch <id>`; an unthrottled spec (`throughput` -1 or unset) shows a target of `—`; GETs per refresh = 3 + running rows (capped at 5) | S |
| 4.2 | The Chaos line: `/api/disruptions?page=0&size=5`. A RUNNING disruption shows plan, age and id, and downgrades A3–A5 to "during chaos". | `dashboard.go`, `dashboard_rules.go`, tests | A RUNNING disruption plus URP gives ⚠ with "during chaos"; none in 24 h reads "none in 24 h" | S |
| 4.3 | VS BASELINE: `/api/tests/baselines` once a minute, plus `report/regression` for each baselined type's latest DONE run, cached by run id. Rule A9. | `dashboard.go`, `dashboard_rules.go`, tests | A finished run is fetched once and never again; `regressionDetected` raises ⚠ with `kates report regression <id>`; no baseline reads "no baseline" | S |
| 4.4 | Brokers up against expected: `/api/cluster/topology` at start and every 5 min. Expected is the sum of replicas of the node pools with a broker role. Rule A4. If the topology fails (no Kubernetes access), the row reads "3 brokers" with no expected count. | `dashboard.go`, `dashboard_rules.go`, tests | 2 of 3 gives ✖ (⚠ during chaos); a topology error falls back without any attention line | S |

### Wave 5: docs

| PR | Change | Files | Size |
| --- | --- | --- | --- |
| 5.1 | Describe what the dashboard answers and how to drill down. Drop the "Grafana equivalent" claim. Show the 80×24 frame, list the keys, and state the role split with `top`, `status` and `cluster watch` (D5). | `docs/book/09-observability.md:345-360`, `docs/book/10-cli-reference.md:1162-1172`, `cli/cmd/tldr.go:75` | XS |

Each wave merges on its own. 2.2 needs 2.1; 3.x needs 2.1; 4.x needs 3.1. 4.1–4.4 are independent of each other.

### Backend changes (separate, optional, deferred)

Deferred with Wave 4, for the same reason.

| # | Change | Why | Size |
| --- | --- | --- | --- |
| B1 | `checkedAt` on `/api/cluster/check` | The 30 s cache makes the age unknowable (U13) | XS |
| B2 | The latest phase metrics (rate, p99) on RUNNING items of `/api/tests` | Removes 4.1's per-run GET, and fills `top`, `badge` and `test summary` too (F27) | S |
| B3 | `GET /api/tests/counts?since=24h` by status and type | Removes the 100-run cap and the "100+" label (D6) | S |
| B4 | `totalLag` in `/api/cluster/groups` items | A consumer-lag attention rule without a GET per group | S |
| B5 | Firing alerts from Alertmanager or Prometheus | `/api/cluster/alerts` lists rules, not what is firing | M |

## 11. Decisions

Taken 2026-10-18 by the maintainer: the recommended pick each time.

| # | Decision | Options | Decided |
| --- | --- | --- | --- |
| D1 | Shell | (a) Bubble Tea model around the pure render; (b) keep the print loop and redraw in place | **(a).** It is the house pattern (`tui.Run`, `deploy.go:301`, `TooSmallCard`); `check-cli-style.sh` keeps raw escapes in `cli/output`; it brings resize, keys, the alt screen and diffed redraws for about 80 lines. (b) needs new cursor-control helpers in `output` and signal handling to leave the alt screen |
| D2 | Borders | Keep the rounded panels, or borderless sections | **Borderless.** Panels cost 2 columns and 2 rows each, caused U2, and add nothing the headers don't |
| D3 | Thresholds | Values for A7, A8 and A9 | **Fixed constants in `dashboard_rules.go`, no flags:** A8 below 90% of target for 2 polls after the first minute; A7 strict live SLA breach; A9 the backend's own `regressionDetected`. Revisit after use. Applies when Wave 4 lands: A7–A9 are Wave 4 rules |
| D4 | Wave 1 scope | Fix only the truth bugs, or also the wrap and alignment | **Truth only (1.1).** Wave 2.1 fixes width and alignment in the new render; fixing panels Wave 3 deletes is churn |
| D5 | Role against `top` | Fold `top` into `dashboard`, or keep both | **Keep both.** The dashboard caps running rows and links to `top`; `top` stays the full table; `status` stays the one-liner for scripts |
| D6 | Window for "finished" | Last N runs or last 24 h | **24 h, from the 100-run page,** shown as "100+ in 24 h" when the page does not reach back far enough. B3 (deferred) would remove the cap |
| D7 | Non-TTY output | Keep the append-only frames, or print one frame and exit | **Keep** (no behavior change). `kates status` and `-o json` commands cover scripts |

## Not checked

- `kates kafka tui` was not captured, because it is not the dashboard. Its tab issues are in the perf plan.
- Light terminal themes and Terminal.app's 256-color mode were not checked by eye. The plan relies on `theme`'s AdaptiveColor and the glyph+word rule.
- `⚠` is East-Asian-ambiguous width in some fonts and may draw 2 columns wide. It is the house Warn glyph, used elsewhere already; if it misaligns, the fix belongs in `output/glyphs.go`, not here.
- Nothing was read from the real Kates API. All captures came from a stub whose shapes come from the backend source on `main`, not from a live cluster.
