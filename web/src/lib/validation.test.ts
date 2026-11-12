import { describe, expect, it } from 'vitest';
import { validateConfirmation, validateFullName, validatePassword, validateResetCode } from './validation';

describe('validation mirrors the API (AC-4, PRD-005 UI-BR-3)', () => {
  it('password is 8 to 72 UTF-8 bytes', () => {
    expect(validatePassword('a'.repeat(7))).toBe('passwordLength');
    expect(validatePassword('a'.repeat(8))).toBeNull();
    expect(validatePassword('a'.repeat(72))).toBeNull();
    expect(validatePassword('a'.repeat(73))).toBe('passwordLength');
    expect(validatePassword('é'.repeat(36))).toBeNull(); // 72 bytes
    expect(validatePassword('é'.repeat(37))).toBe('passwordLength'); // 74 bytes
    expect(validatePassword('ééé')).toBe('passwordLength'); // 6 bytes
  });

  it('full name is 2 to 100 characters after trimming', () => {
    expect(validateFullName('  A ')).toBe('nameLength');
    expect(validateFullName('Al')).toBeNull();
    expect(validateFullName('أحمد')).toBeNull();
    expect(validateFullName('x'.repeat(100))).toBeNull();
    expect(validateFullName('x'.repeat(101))).toBe('nameLength');
    expect(validateFullName('')).toBe('nameLength');
  });

  it('reset code is exactly 6 digits', () => {
    expect(validateResetCode('123456')).toBeNull();
    expect(validateResetCode('12345')).toBe('codeFormat');
    expect(validateResetCode('12a456')).toBe('codeFormat');
    expect(validateResetCode('1234567')).toBe('codeFormat');
  });

  it('confirmation must match', () => {
    expect(validateConfirmation('secret123', 'secret123')).toBeNull();
    expect(validateConfirmation('secret123', 'secret124')).toBe('passwordsDiffer');
  });
});
