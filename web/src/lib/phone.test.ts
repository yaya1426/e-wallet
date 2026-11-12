import { describe, expect, it } from 'vitest';
import { formatPhone, normalizeEgyptianMobile } from './phone';

describe('normalizeEgyptianMobile (AC-2, PRD-005 UI-BR-2)', () => {
  it.each([
    ['01001234567', '+201001234567'],
    ['201001234567', '+201001234567'],
    ['+201001234567', '+201001234567'],
    ['+20 100 123 4567', '+201001234567'],
    ['0100-123-4567', '+201001234567'],
    [' 01112345678 ', '+201112345678'],
    ['01212345678', '+201212345678'],
    ['01512345678', '+201512345678'],
  ])('accepts %j', (input, want) => {
    expect(normalizeEgyptianMobile(input)).toBe(want);
  });

  it.each([
    '01301234567', // 013 is not a mobile prefix
    '0100123456', // 10 digits
    '010012345678', // 12 digits
    '+2010012345678',
    '0100123456a',
    'abc',
    '',
    '+1 202 555 0100',
    '00201001234567',
  ])('rejects %j', (input) => {
    expect(normalizeEgyptianMobile(input)).toBeNull();
  });
});

describe('formatPhone', () => {
  it('groups an Egyptian E.164 number', () => {
    expect(formatPhone('+201001234567')).toBe('+20 100 123 4567');
    expect(formatPhone('weird')).toBe('weird');
  });
});
