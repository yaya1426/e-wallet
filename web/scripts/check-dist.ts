// Build check (T030 AC-18): dist/index.html carries the exact CSP and no inline script.
import { readdirSync, readFileSync } from 'node:fs';
import { CONTENT_SECURITY_POLICY } from '../csp.ts';

const html = readFileSync(new URL('../dist/index.html', import.meta.url), 'utf8');
const failures: string[] = [];

const meta = /<meta http-equiv="Content-Security-Policy" content="([^"]*)"/.exec(html);
if (!meta) failures.push('no Content-Security-Policy meta tag');
else {
  const content = (meta[1] ?? '').replaceAll('&#39;', "'").replaceAll('&quot;', '"').replaceAll('&amp;', '&');
  if (content !== CONTENT_SECURITY_POLICY) failures.push(`CSP differs: ${content}`);
}

for (const m of html.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/g)) {
  const [, attrs = '', body = ''] = m;
  if (!/\bsrc=/.test(attrs) || body.trim() !== '') failures.push(`inline script: ${m[0].slice(0, 80)}`);
}
if (/\sstyle=/.test(html) || /<style\b/.test(html)) failures.push('inline style in index.html');

// T034 AC-11 / TDR-022 §5: operator code is only in the lazily loaded /ops chunk.
const entry = /<script[^>]*\ssrc="\/?([^"]+\.js)"/.exec(html)?.[1];
if (!entry) failures.push('no entry script in index.html');
else {
  // Everything the first page load fetches: the entry and its preloaded chunks.
  const startup = [entry, ...[...html.matchAll(/<link[^>]*rel="modulepreload"[^>]*href="\/?([^"]+)"/g)].map((m) => m[1] ?? '')];
  for (const file of startup) {
    if (readFileSync(new URL(`../dist/${file}`, import.meta.url), 'utf8').includes('X-Internal-Api-Key')) {
      failures.push(`operator code in a startup chunk (${file})`);
    }
  }
  const assets = readdirSync(new URL('../dist/assets/', import.meta.url)).filter((f) => f.endsWith('.js'));
  const opsChunk = assets.some((f) => readFileSync(new URL(`../dist/assets/${f}`, import.meta.url), 'utf8').includes('X-Internal-Api-Key'));
  if (!opsChunk) failures.push('no chunk contains the operator client (check the test itself)');
}

if (failures.length > 0) {
  console.error(`check-dist failed:\n- ${failures.join('\n- ')}`);
  process.exit(1);
}
console.log('check-dist: CSP present, no inline scripts or styles, operator console in its own chunk');
