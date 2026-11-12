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

export const NOTE_MAX = 140;

/** Characters the API counts for a transfer note: code points after trimming (Go TrimSpace + RuneCount). */
export function noteLength(note: string): number {
  return [...note.trim()].length;
}

/** Optional transfer note: at most 140 characters after trimming (PRD-005 UI-BR-28). */
export function validateNote(note: string): ValidationKey | null {
  return noteLength(note) > NOTE_MAX ? 'noteLength' : null;
}
