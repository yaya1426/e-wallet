// PRD-005 UI-BR-2: accept 01XXXXXXXXX, 201XXXXXXXXX or +201XXXXXXXXX (spaces and dashes
// ignored) and return E.164 as the API expects (^\+201[0125][0-9]{8}$), or null.
const apiPattern = /^\+201[0125][0-9]{8}$/;

export function normalizeEgyptianMobile(input: string): string | null {
  const compact = input.replace(/[\s-]/g, '');
  let e164: string;
  if (/^01\d{9}$/.test(compact)) {
    e164 = `+2${compact}`;
  } else if (/^201\d{9}$/.test(compact)) {
    e164 = `+${compact}`;
  } else if (/^\+201\d{9}$/.test(compact)) {
    e164 = compact;
  } else {
    return null;
  }
  return apiPattern.test(e164) ? e164 : null;
}

/** "+201001234567" → "+20 100 123 4567" for display; anything else is returned unchanged. */
export function formatPhone(e164: string): string {
  const m = /^\+20(\d{3})(\d{3})(\d{4})$/.exec(e164);
  return m ? `+20 ${m[1]} ${m[2]} ${m[3]}` : e164;
}
