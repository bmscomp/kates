// Does every panel actually RENDER on this Grafana, or does the frontend
// refuse it?
//
// The API check proves a board was accepted and stored. It says nothing about
// whether the browser can draw it: a panel type the frontend does not know
// renders "Panel plugin not found", and a schemaVersion from the future is
// stored verbatim and then interpreted by whatever migrations that frontend
// happens to have. Both look fine over the API.
//
// Usage: node render.js <grafana-url> <boards-dir>

const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const URL = process.argv[2] || 'http://127.0.0.1:3123';
const DIR = process.argv[3] || path.join(__dirname, 'boards');

// Grafana 8 through 12 have all carried one of these two. Kept in one place
// because the wait and the count have to agree: waiting on a selector the
// counter does not use is how a page "loads" with nothing on it.
const PANEL_SELECTOR =
  '[data-testid^="data-testid Panel header"], .panel-container';

(async () => {
  // Playwright's own download if it has one; otherwise a Chromium the image
  // already carries, which is how this runs in CI and in a sandbox where
  // `playwright install` is not allowed.
  const launch = { args: ['--no-sandbox', '--disable-dev-shm-usage'] };
  const candidates = [
    process.env.CHROMIUM_PATH,
    ...(fs.existsSync('/opt/pw-browsers')
      ? fs.readdirSync('/opt/pw-browsers')
          .filter(d => d.startsWith('chromium-'))
          .map(d => `/opt/pw-browsers/${d}/chrome-linux/chrome`)
      : []),
    '/usr/bin/chromium', '/usr/bin/chromium-browser', '/usr/bin/google-chrome',
  ].filter(Boolean);
  const found = candidates.find(p => { try { return fs.existsSync(p); } catch { return false; } });
  if (found) launch.executablePath = found;
  const browser = await chromium.launch(launch);
  const context = await browser.newContext({
    viewport: { width: 1600, height: 1200 },
    httpCredentials: { username: 'admin', password: 'admin' },
  });

  let totalPanels = 0, totalPluginMissing = 0, totalErrors = 0, boards = 0;
  const problems = [];

  for (const file of fs.readdirSync(DIR).filter(f => f.endsWith('.json')).sort()) {
    const board = JSON.parse(fs.readFileSync(path.join(DIR, file), 'utf8'));
    const page = await context.newPage();
    const consoleErrors = [];
    page.on('console', m => {
      if (m.type() === 'error') {
        const t = m.text();
        // Noise from the environment, not from the board.
        //
        // The second pattern is the one worth explaining. Grafana 11.5+ ships
        // bundled apps it fetches at boot; with no route to grafana.com they
        // are registered but absent, and the frontend logs
        //   Could not load plugin: 404 ... /public/plugins/<app>/module.js
        // on EVERY page. It surfaced the moment the pages started loading at
        // all, reporting the identical three errors on all twelve boards —
        // which is what says it is page-level rather than board-level. The
        // harness now turns the preinstall off, so this is a backstop for an
        // environment that still manages to produce it.
        //
        // A panel plugin that is genuinely missing is NOT filtered here: it is
        // counted separately as pluginMissing, from the rendered text.
        if (/grafana\.com|favicon|analytics|ERR_CERT|Failed to load resource/i.test(t)) return;
        if (/Could not load plugin|\/public\/plugins\/[^\s]*\/module\.js|SystemJS Error/i.test(t)) return;
        consoleErrors.push(t.slice(0, 200));
      }
    });
    page.on('pageerror', e => consoleErrors.push('pageerror: ' + String(e).slice(0, 200)));

    // NOT `waitUntil: 'networkidle'`. Grafana's frontend keeps connections
    // open — live channels, streaming, scenes' own polling — so "no network
    // activity for 500ms" is a condition that may simply never hold. It held
    // on 9.5, 10.4 and 12.3 and did not on 11.6, where every one of the twelve
    // pages hit the 60s timeout and the job reported 0 boards rendered while
    // the API half of the same run had just loaded all 12. A wait condition
    // that depends on the app going quiet is the wrong wait for an app that
    // does not.
    //
    // `domcontentloaded` plus a wait for a panel to actually appear is both
    // deterministic and much faster: it waits for the thing being tested.
    try {
      await page.goto(`${URL}/d/${board.uid}?kiosk&from=now-1h&to=now`,
                      { waitUntil: 'domcontentloaded', timeout: 45000 });
      await page.waitForSelector(PANEL_SELECTOR, { timeout: 45000 });
    } catch (e) {
      // A screenshot of the failure beats a bare timeout: "Grafana never
      // painted a panel" and "the board is broken" read identically in a log
      // line and not at all in a picture.
      const shot = `/tmp/compat-fail-${file.replace(/\.json$/, '')}.png`;
      try { await page.screenshot({ path: shot }); } catch {}
      problems.push(`${file}: no panel appeared — ${String(e).split('\n')[0].slice(0, 140)}` +
                    ` (screenshot: ${shot})`);
      await page.close();
      continue;
    }

    // Panels render lazily as they scroll into view. Scroll until the count
    // stops growing rather than a fixed number of times — the fixed loop spent
    // 8 seconds per board whether or not anything was still to come, which on
    // four versions and twelve boards was most of a 22-minute step.
    let seenCount = -1;
    for (let i = 0; i < 20; i++) {
      const n = await page.locator(PANEL_SELECTOR).count();
      if (n === seenCount && i > 1) break;
      seenCount = n;
      await page.mouse.wheel(0, 1400);
      await page.waitForTimeout(400);
    }
    await page.waitForTimeout(1200);

    const seen = await page.evaluate((SEL) => {
      const text = document.body.innerText || '';
      const count = (re) => (text.match(re) || []).length;
      return {
        panels: document.querySelectorAll(SEL).length,
        pluginMissing: count(/Panel plugin not found/gi),
        panelError: count(/An unexpected error happened/gi),
        // A datasource that cannot be reached is a DATA problem, not a render
        // problem, and is counted separately so it is not read as a board bug.
        dataError: count(/Error updating options|Query error|Bad Gateway|dial tcp/gi),
      };
    }, PANEL_SELECTOR);

    boards++;
    totalPanels += seen.panels;
    totalPluginMissing += seen.pluginMissing;
    totalErrors += seen.panelError;
    if (seen.pluginMissing) {
      problems.push(`${file}: ${seen.pluginMissing} panel(s) say "Panel plugin not found"`);
    }
    if (seen.panelError) {
      problems.push(`${file}: ${seen.panelError} panel(s) show an unexpected error`);
    }
    if (consoleErrors.length) {
      problems.push(`${file}: ${consoleErrors.length} console error(s), first: ${consoleErrors[0]}`);
    }
    console.log(`  ${file.padEnd(30)} ${String(seen.panels).padStart(3)} panels rendered` +
                (seen.pluginMissing ? `  ${seen.pluginMissing} PLUGIN MISSING` : '') +
                (seen.panelError ? `  ${seen.panelError} ERROR` : '') +
                (seen.dataError ? `  (${seen.dataError} data warnings)` : ''));
    await page.close();
  }

  console.log(`\n  ${boards} boards, ${totalPanels} panels rendered, ` +
              `${totalPluginMissing} missing plugins, ${totalErrors} panel errors`);
  for (const p of problems) console.log(`    PROBLEM: ${p}`);
  await browser.close();
  process.exit(problems.length ? 1 : 0);
})();
