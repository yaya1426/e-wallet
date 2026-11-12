import { en } from '@/i18n/en';
import type { EndReason } from '@/auth/session';

/** Router state passed to /login by the other auth screens. */
export interface LoginLocationState {
  phone?: string;
  notice?: 'accountCreated' | 'passwordChanged';
}

export function endReasonNotice(reason: EndReason | null): { tone: 'info' | 'error'; text: string } | null {
  switch (reason) {
    case 'sessionEnded':
      return { tone: 'info', text: en.notices.sessionEnded };
    case 'userDisabled':
      return { tone: 'error', text: en.notices.userDisabled };
    case 'signedOut':
      return { tone: 'info', text: en.notices.signedOut };
    case 'network':
      return { tone: 'error', text: en.errors.NETWORK_ERROR ?? '' };
    default:
      return null;
  }
}
