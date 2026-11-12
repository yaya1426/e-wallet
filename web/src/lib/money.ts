// PRD-005 UI-BR-1, PRD-001 BR-2: money is integer piasters (1 EGP = 100). No floating point
// arithmetic is ever applied to an amount.

/** Formats piasters as "EGP 1,234.56" (negative: "-EGP 1.50"). */
export function formatMoney(minor: number): string {
  if (!Number.isSafeInteger(minor)) {
    throw new RangeError(`formatMoney: not a safe integer: ${minor}`);
  }
  const negative = minor < 0;
  const digits = String(Math.abs(minor)).padStart(3, '0');
  const pounds = digits.slice(0, -2).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const piasters = digits.slice(-2);
  return `${negative ? '-' : ''}EGP ${pounds}.${piasters}`;
}

export type ParseAmountError = 'empty' | 'format' | 'tooManyDecimals' | 'zero' | 'tooLarge';
export type ParseAmountResult = { ok: true; minor: number } | { ok: false; error: ParseAmountError };

/**
 * Parses a user-typed EGP amount ("150", "150.5", "1,234.56") into piasters using string and
 * integer arithmetic only. Business limits (PRD-001 §5.6) are checked by the API.
 */
export function parseAmount(text: string): ParseAmountResult {
  const cleaned = text.trim().replace(/[,\s]/g, '');
  if (cleaned === '') return { ok: false, error: 'empty' };
  if (/^\d+\.\d{3,}$/.test(cleaned)) return { ok: false, error: 'tooManyDecimals' };
  const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(cleaned);
  if (!match) return { ok: false, error: 'format' };
  const whole = match[1] ?? '0';
  const fraction = (match[2] ?? '').padEnd(2, '0');
  const digits = (whole + fraction).replace(/^0+(?=\d)/, '');
  if (/^0+$/.test(digits)) return { ok: false, error: 'zero' };
  if (digits.length > 16) return { ok: false, error: 'tooLarge' };
  const minor = Number(digits);
  if (!Number.isSafeInteger(minor)) return { ok: false, error: 'tooLarge' };
  return { ok: true, minor };
}
