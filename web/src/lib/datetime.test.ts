import { describe, expect, it } from 'vitest';
import { formatDateTime } from './datetime';

describe('formatDateTime (UI-BR-17)', () => {
  it.each([
    ['2026-11-12T12:05:00Z', '12 Nov 2026, 14:05'],
    ['2026-01-01T22:30:00Z', '2 Jan 2026, 00:30'], // crosses midnight in Cairo
    ['2026-07-15T09:00:00Z', '15 Jul 2026, 12:00'], // summer time (UTC+3)
  ])('%s → %s', (iso, want) => {
    expect(formatDateTime(iso)).toBe(want);
  });

  it('returns invalid input unchanged', () => {
    expect(formatDateTime('nope')).toBe('nope');
  });
});
