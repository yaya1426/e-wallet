/**
 * Only same-app paths are allowed as a post-login destination (no open redirect). The value is
 * resolved like the browser would (which drops tabs/newlines and treats "\" as "/") and must stay
 * on this origin.
 */
export function safeNext(next: string | null | undefined, origin = window.location.origin): string {
  if (!next || !next.startsWith('/') || next.includes('\\') || hasControlCharacter(next)) return '/';
  let url: URL;
  try {
    url = new URL(next, origin);
  } catch {
    return '/';
  }
  if (url.origin !== origin) return '/';
  return url.pathname + url.search + url.hash;
}

function hasControlCharacter(value: string): boolean {
  for (let i = 0; i < value.length; i++) {
    const c = value.charCodeAt(i);
    if (c < 0x20 || c === 0x7f) return true;
  }
  return false;
}
