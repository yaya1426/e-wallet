// Egyptian IBAN helpers (PRD-005 UI-BR-20). Mirrors the API's check: after removing spaces and
// upper-casing, ^EG[0-9]{27}$ and a valid ISO 13616 mod-97 check (remainder 1).
const egyptianIban = /^EG\d{27}$/;

export function normalizeIban(input: string): string {
  return input.replaceAll(' ', '').toUpperCase(); // spaces only, like the API
}

/** ISO 13616 mod-97 with chunked integer math (no BigInt, no floats). */
function mod97(iban: string): number {
  const rearranged = iban.slice(4) + iban.slice(0, 4);
  const digits = rearranged.replace(/[A-Z]/g, (c) => String(c.charCodeAt(0) - 55));
  let remainder = 0;
  for (let i = 0; i < digits.length; i += 7) {
    remainder = Number(String(remainder) + digits.slice(i, i + 7)) % 97;
  }
  return remainder;
}

export function isValidEgyptianIban(input: string): boolean {
  const iban = normalizeIban(input);
  return egyptianIban.test(iban) && mod97(iban) === 1;
}

/** "EG890003…" → "EG89 0003 …" (groups of four). */
export function formatIban(iban: string): string {
  return normalizeIban(iban).replace(/(.{4})/g, '$1 ').trim();
}

export function ibanLast4(iban: string): string {
  return normalizeIban(iban).slice(-4);
}
