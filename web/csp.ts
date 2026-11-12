// The Content-Security-Policy of the built app (TDR-020 §4). Injected into dist/index.html at
// build time only: Vite's dev server needs inline styles/scripts for hot reload.
export const CONTENT_SECURITY_POLICY =
  "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'";
