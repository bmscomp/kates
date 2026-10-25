# katesplatforme.io

The marketing site for Kates. Plain HTML, CSS and one small script, no build step: the
folder is the site. The book at <https://bmscomp.github.io/kates/> stays where it is; this
site links to it.

```
website/
├── index.html      the one page
├── styles.css      the Paper theme (light, EB Garamond headings) and the layout
├── themes/         alternative palettes (navy, phosphor, ember, slate); add one <link> after styles.css to switch
├── site.js         mobile menu and footer year
├── _headers        security and cache headers (Cloudflare Pages, Netlify)
├── robots.txt, sitemap.xml
└── assets/         logo, wordmark, screenshots, social preview (copied from docs/)
```

## Preview locally

```bash
cd website && python3 -m http.server 8000
# open http://localhost:8000
```

## Deploy

The site is deployed from this folder by `.github/workflows/website.yml`, which validates
the HTML on every pull request and publishes to Cloudflare Pages on a push to `main`.
Setup, once:

1. Create a Cloudflare Pages project named `kates-website` (direct upload, no Git
   integration: the workflow uploads the folder).
2. Add two repository secrets: `CLOUDFLARE_API_TOKEN` (a token with *Cloudflare Pages:
   Edit*) and `CLOUDFLARE_ACCOUNT_ID`.
3. In the Pages project, add the custom domain `katesplatforme.io` and `www.katesplatforme.io`.
   If the domain's DNS is on Cloudflare the records are created for you; otherwise add a
   `CNAME` for both names pointing at `kates-website.pages.dev`.

Without the secrets the deploy step is skipped and only the validation runs, so the
workflow is safe on forks. [`plans/website.md`](../plans/website.md) has the full plan,
including the alternatives (Netlify, GitHub Pages on a second repository).
