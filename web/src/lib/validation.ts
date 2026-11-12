// Client-side checks that mirror the API (PRD-005 UI-BR-3). The API stays the authority.
// Each function returns null when the value is valid, or a key of en.validation.
import type { en } from '@/i18n/en';

export type ValidationKey = keyof typeof en.validation;

const encoder = new TextEncoder();

export function validatePassword(password: string): ValidationKey | null {
  const bytes = encoder.encode(password).length;
  return bytes < 8 || bytes > 72 ? 'passwordLength' : null;
}

export function validateFullName(name: string): ValidationKey | null {
  const length = [...name.trim()].length;
  return length < 2 || length > 100 ? 'nameLength' : null;
}

export function validateResetCode(code: string): ValidationKey | null {
  return /^\d{6}$/.test(code.trim()) ? null : 'codeFormat';
}

export function validateConfirmation(password: string, confirmation: string): ValidationKey | null {
  return password === confirmation ? null : 'passwordsDiffer';
}
