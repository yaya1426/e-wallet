import { describe, expect, it } from 'vitest';
import { formatMoney, parseAmount } from './money';

describe('formatMoney (AC-3, PRD-005 UI-BR-1)', () => {
  it.each([
    [0, 'EGP 0.00'],
    [5, 'EGP 0.05'],
    [99, 'EGP 0.99'],
    [100, 'EGP 1.00'],
    [15075, 'EGP 150.75'],
    [123456789, 'EGP 1,234,567.89'],
    [-150, '-EGP 1.50'],
    [Number.MAX_SAFE_INTEGER, 'EGP 90,071,992,547,409.91'],
  ])('%d → %s', (minor, want) => {
    expect(formatMoney(minor)).toBe(want);
  });

  it.each([1.5, Number.NaN, Number.POSITIVE_INFINITY, Number.MAX_SAFE_INTEGER + 1])('throws on %d', (v) => {
    expect(() => formatMoney(v)).toThrow(RangeError);
  });
});

describe('parseAmount', () => {
  it.each([
    ['150', 15000],
    ['150.5', 15050],
    ['150.75', 15075],
    ['1,234.56', 123456],
    ['0.01', 1],
    [' 12 ', 1200],
    ['007.10', 710],
    ['0.10', 10],
  ])('%j → %d piasters', (text, minor) => {
    expect(parseAmount(text)).toEqual({ ok: true, minor });
  });

  it.each([
    ['', 'empty'],
    ['   ', 'empty'],
    ['abc', 'format'],
    ['-5', 'format'],
    ['1.', 'format'],
    ['.5', 'format'],
    ['1e3', 'format'],
    ['1.234', 'tooManyDecimals'],
    ['0', 'zero'],
    ['0.00', 'zero'],
    ['99999999999999999', 'tooLarge'],
  ])('%j → %s', (text, error) => {
    expect(parseAmount(text)).toEqual({ ok: false, error });
  });

  it('never goes through floating point (0.29 is exactly 29)', () => {
    expect(parseAmount('0.29')).toEqual({ ok: true, minor: 29 });
    expect(parseAmount('1.15')).toEqual({ ok: true, minor: 115 });
  });
});
