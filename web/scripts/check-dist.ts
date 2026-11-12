// Build check (T030 AC-18): dist/index.html carries the exact CSP and no inline script.
import { readFileSync } from 'node:fs';
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

if (failures.length > 0) {
  console.error(`check-dist failed:\n- ${failures.join('\n- ')}`);
  process.exit(1);
}
console.log('check-dist: CSP present, no inline scripts or styles');
