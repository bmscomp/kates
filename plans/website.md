# katesplatforme.io — Website Plan (2026-10-25)

**Status: first version in `website/`, deploy workflow in `.github/workflows/website.yml`, DNS and Cloudflare project still to be created by the domain owner.**

Goal: a public site at **katesplatforme.io** that promotes Kates as a technology (performance testing, chaos engineering and security auditing for Apache Kafka on Kubernetes), sends visitors to the quick start, the book and the GitHub repository, and costs nothing to host or maintain. The book at <https://bmscomp.github.io/kates/> is the reference manual and keeps its URL; the website is the front door to it.

## 1. Audience and message

| Visitor | Arrives from | Wants in 10 seconds | The site answers with |
|:--|:--|:--|:--|
| Platform engineer or SRE running Kafka on Kubernetes | Search, a conference talk, a link in a Slack channel | "Does this solve my problem, and is it serious?" | Hero headline, the three problem areas (performance, resilience, data integrity), the comparison table |
| Developer evaluating tools | GitHub, Artifact Hub, the README | "How fast can I try it?" | Five-command quick start, Homebrew one-liner, GitHub Action snippet |
| Engineering manager or architect | A colleague's recommendation | "What is it made of, who maintains it, what does it depend on?" | Architecture diagram, "built on" strip (Strimzi, LitmusChaos, Quarkus, Helm, Prometheus), Apache 2.0, links to releases and contributing |

One message, repeated in every section: *you find out how a Kafka cluster handles a failure on the day it fails, unless you have already tried it.* Kates lets you try it first, repeatably, and grades the result.

Every claim on the page comes from the README or the book: eight test types, thirteen disruption types, A–F grades, CIS / SOC 2 / PCI DSS mapping, the three-zone Kind lab, twelve Grafana dashboards. The page states no number the repository does not.

## 2. Design

### Identity

- **Palette**: the "Paper" theme, chosen on 2026-10-25 from five rendered variants (Navy, Paper, Phosphor, Ember, Slate). Off-white page `#f7f5ef`, white cards, navy ink `#102a66` for headings (the logo's navy), text `#2b3350`, accent `#1a5bcc` (the book's link blue), green `#1f8a5b` for passing gates, amber for a degraded number. Terminals and code blocks stay dark (`#0a0f1d`) so the CLI output reads as a screen on a page. The footer is solid navy. The other four palettes are kept as drop-in override stylesheets in `website/themes/`; adding one `<link>` after `styles.css` switches the whole site.
- **Type**: EB Garamond for `h1` and `h2` and the wordmark, as in the book's chapters; Inter for everything else; JetBrains Mono for every command, grade and metric.
- **Marks**: the ring-and-streams logo (`docs/assets/kates-logo.svg`) in the nav, the CTA and as favicon; the dark wordmark in the footer; `docs/assets/social-preview.png` as the Open Graph card. The site copies these into `website/assets/` so the folder deploys on its own.
- **Tone**: plain, declarative, no superlatives. Terminal output and YAML carry the proof; prose explains why it matters.

### Page structure (single page, anchored sections)

1. **Nav**: logo, Features, How it works, Architecture, Compare, CI gates, Docs, GitHub, "Get started". Sticky, collapses to a menu under 760 px.
2. **Hero**: headline "Break your Kafka cluster before production does", the README's opening paragraph as lead, two CTAs (Quick start, Read the book), and a terminal showing `kates health`, a LOAD test with P50/P95/P99 and a grade, a resilience run with before/after P99 and recovery time, and `kates gate` passing. The terminal is the product demo; the GIF in `docs/assets/demo.gif` (3.7 MB) is deliberately not on the page to keep it light.
3. **Built-on strip**: Apache Kafka, Strimzi, LitmusChaos, Quarkus, Helm, Prometheus & Grafana, Kind. Text only; no third-party logos until their trademark terms are checked.
4. **Why Kates**: the two-terminals-and-a-hunch problem, then three cards for performance, resilience and data integrity, the three branches of the book's readiness diagram.
5. **Features**: six cards mirroring the README's "What it does": performance tests, fault injection, resilience runs and disruption plans, security audit, CI gates, the multi-zone lab with its dashboards.
6. **Quick start**: the README's five steps as a numbered list beside the exact commands, plus the GitHub Action snippet.
7. **Architecture**: an inline SVG (CLI → Kates API → Kafka / LitmusChaos / PostgreSQL / Prometheus-Grafana, inside a dashed Kubernetes boundary, "installed by signed Helm charts"), and the two screenshots from the book (`kates-cli-overview.png`, `kates-lab-tui.png`).
8. **Compare**: the README's comparison table against `kafka-producer-perf-test`, OpenMessaging Benchmark and manual Litmus / `kubectl`, with the Kates column highlighted.
9. **CI gates**: a scenario file with SLA gates and a disruption, and the terminal output of `kates test apply -f … --wait` passing each gate.
10. **Final CTA**: star on GitHub, read the book, charts on Artifact Hub, the Homebrew one-liner.
11. **Footer**: wordmark, three link columns (Project, Learn, Ecosystem), license and the Apache trademark notice.

### Engineering choices

- **No framework, no build.** Three files (`index.html`, `styles.css`, `site.js`) and an `assets/` folder. Anyone can edit the copy in a pull request and preview it with `python3 -m http.server`. A static-site generator can be introduced later if the site grows pages (blog, case studies); it is not needed for one.
- **Performance budget**: under 1 MB on first load excluding the two lazy-loaded screenshots; Google Fonts with `display=swap`; images below the fold `loading="lazy"`; assets cached one year by `_headers`.
- **Accessibility**: one `h1`, landmarks, skip link, `aria-label` on the terminal and the diagram, every text colour over 4.5:1 on its surface (the same pairs the book checks in CI), focus visible, `prefers-reduced-motion` respected, 16 px side gutters and no horizontal scroll at 360 px.
- **SEO**: title, description, canonical, Open Graph and Twitter cards, `SoftwareApplication` JSON-LD, `robots.txt`, `sitemap.xml`.
- **Security headers** in `_headers`: a CSP that allows only self, Google Fonts and inline styles; `frame-ancestors 'none'`; `nosniff`; referrer policy. The JSON-LD block is data, not script, so `script-src 'self'` holds.

## 3. Hosting and deployment

### Where

GitHub Pages serves one site per repository and `bmscomp.github.io/kates` already belongs to the book, so the website cannot share it without moving the book or creating a second repository. The recommendation is **Cloudflare Pages**:

| Option | Cost | Custom domain + TLS | Fits this repo | Notes |
|:--|:--|:--|:--|:--|
| **Cloudflare Pages (recommended)** | Free, unlimited bandwidth | Yes, automatic | Direct upload of `website/` from a workflow; no Git integration needed | Fast global edge, `_headers` supported, preview URLs per branch if wanted |
| Netlify | Free tier (100 GB/month) | Yes, automatic | Same folder, `netlify deploy --dir=website --prod` | `_headers` works unchanged; bandwidth cap is the only difference |
| GitHub Pages on a second repository (`bmscomp/katesplatforme.io`) | Free | Yes, via `CNAME` file | Needs a sync workflow that pushes `website/` to the other repo | Keeps everything on GitHub; one more repository to maintain |
| GitHub Pages on this repository | Free | Yes | Would replace the book's URL | Rejected: breaks every link to the book |

### How (Cloudflare Pages)

1. **Project.** In the Cloudflare dashboard, Workers & Pages → Create → Pages → *Direct upload*, name `kates-website`. The first upload can be empty; the workflow fills it.
2. **Token.** My Profile → API Tokens → Create Token → *Edit Cloudflare Workers* template, or a custom token with *Account · Cloudflare Pages · Edit*. Copy the account ID from the dashboard's URL or the Workers & Pages overview.
3. **Secrets.** Repository Settings → Secrets and variables → Actions: `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`. Optionally create a GitHub *environment* named `website` and move the secrets there so a deploy can require a reviewer.
4. **Domain.** Pages project → Custom domains → add `katesplatforme.io` and `www.katesplatforme.io`. With the zone on Cloudflare, the `CNAME` records are created automatically and `www` can be redirected to the apex with a Bulk Redirect or a Page Rule. With DNS elsewhere, add `CNAME katesplatforme.io → kates-website.pages.dev` (or `ALIAS`/`ANAME` at the apex where the registrar supports it) and the same for `www`, then wait for the certificate.
5. **Deploy.** Merge to `main`. `.github/workflows/website.yml` validates the HTML with `html-validate`, checks every local `src`/`href` exists, and runs `wrangler pages deploy website --project-name=kates-website`. Without the two secrets the deploy step is skipped and only the validation runs, so forks and the first pull request stay green.

### Workflow behaviour

- Pull requests touching `website/**` run validation only.
- Pushes to `main` validate then deploy; `workflow_dispatch` allows a manual redeploy.
- Action versions are pinned to commit SHAs with the tag in a comment, as the other workflows do.
- The folder has no build, so there is no dependency to update besides the `html-validate` major pinned in the workflow.

## 4. Verification before and after launch

- `npx html-validate website/index.html` passes; the page renders without horizontal scroll at 360, 768 and 1440 px (checked with Playwright in this session).
- Lighthouse after deploy: aim for ≥ 95 on Performance, Accessibility, Best Practices and SEO. The fonts and the two PNG screenshots are the only external or heavy resources.
- Open Graph check with the Twitter/X card validator or <https://opengraph.xyz> once the domain serves `assets/social-preview.png`.
- `curl -I https://katesplatforme.io` shows the `_headers` values; <https://securityheaders.com> grades it.
- Search Console: add the property, submit `sitemap.xml`.

## 5. Later, if wanted

- **Analytics**: Cloudflare Web Analytics (free, no cookies, one `<script>`), which would need `static.cloudflareinsights.com` added to the CSP. Nothing is included now.
- **Multilingual**: the project is Francophone-friendly; a `/fr/` copy of the page is a second HTML file and a `hreflang` pair. Worth doing once the English copy settles.
- **More pages**: a changelog pulled from GitHub Releases, a "Kates in CI" page with full pipeline examples, a page per integration (Strimzi, Litmus, Trogdor). When the site passes three pages, move it to a generator (Astro or Eleventy) and keep the same palette tokens.
- **Downloads**: Homebrew is the install path; the release tarball links can be added to the CTA as a `<details>` for Linux users without Homebrew.
- **Trademarks**: before showing partner logos in the "built on" strip, confirm each project's logo policy (Apache's is the strictest). Text names are fine and are what ships today.

## 6. Ownership

| Item | Who | When |
|:--|:--|:--|
| Cloudflare Pages project, token, secrets, DNS | Domain owner (`bmscomp`) | Before merging the PR, or right after: the first push to `main` deploys |
| Copy and design changes | Any contributor, by pull request to `website/` | Ongoing; the workflow validates each PR |
| Keeping numbers in sync with the README (test types, disruption types, dashboards) | Whoever changes the README | Add `website/index.html` to the review checklist of README changes |
