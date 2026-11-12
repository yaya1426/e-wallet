import { describe, expect, it } from 'vitest';
import { formatIban, ibanLast4, isValidEgyptianIban, normalizeIban } from './iban';

describe('Egyptian IBAN (T032 AC-1, UI-BR-20)', () => {
  it.each([
    'EG890003000100000000123450000',
    'EG620003000100000000123450001',
    'EG350003000100000000123450002',
    'eg89 0003 0001 0000 0000 1234 5000 0',
  ])('accepts %s', (iban) => {
    expect(isValidEgyptianIban(iban)).toBe(true);
  });

  it.each([
    ['wrong check digits', 'EG880003000100000000123450000'],
    ['too short', 'EG89000300010000000012345000'],
    ['too long', 'EG8900030001000000001234500000'],
    ['not Egypt', 'DE89370400440532013000'],
    ['letters in BBAN', 'EG8900030001000000001234500A0'],
    ['empty', ''],
  ])('rejects %s', (_name, iban) => {
    expect(isValidEgyptianIban(iban)).toBe(false);
  });

  it('normalizes, formats and takes the last four', () => {
    expect(normalizeIban(' eg89 0003 ')).toBe('EG890003');
    expect(formatIban('EG890003000100000000123450000')).toBe('EG89 0003 0001 0000 0000 1234 5000 0');
    expect(ibanLast4('EG890003000100000000123450000')).toBe('0000');
  });
});
