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

### GitHub Pages at https://bmscomp.github.io/katesplatform.io/

The same workflow also publishes the folder to a second repository,
`bmscomp/katesplatform.io`, whose GitHub Pages site is served at that URL. This
repository's own Pages slot is the book, which is why a second repository is needed. The
page uses relative links only, so it works under the `/katesplatform.io/` path as it does at
the domain root. Setup, once:

1. Create the repository `bmscomp/katesplatform.io` (public, empty is fine).
2. Create a fine-grained personal access token restricted to that repository with
   *Contents: Read and write*, and store it in this repository as the secret
   `KATESPLATFORM_PAGES_TOKEN`.
3. Push to `main` once (or run the *Website* workflow by hand). The job commits the folder
   to the other repository's `main` branch.
4. In `bmscomp/katesplatform.io` → Settings → Pages, the source is *Deploy from a
   branch*, branch `main`, folder `/ (root)`; this is already set. The site updates a
   minute after each deploy.

Both deploys are independent: each runs when its secret is present and skips otherwise.
`.nojekyll` in this folder stops GitHub Pages from running Jekyll, which would otherwise drop
`_headers` and anything else starting with an underscore. GitHub Pages ignores `_headers`
anyway: the security headers apply only on Cloudflare or Netlify.
