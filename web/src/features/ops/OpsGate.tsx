// UI-BR-31: no key → the unlock screen. The candidate key is checked against health before it
// is stored.
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { unwrap } from '@/api/client';
import { isApiError } from '@/api/errors';
import { OPS_KEY_HEADER } from '@/api/opsClient';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { PasswordField } from '@/components/PasswordField';
import { en } from '@/i18n/en';
import { messageForError } from '@/lib/errorMessages';
import { useOps, useOpsLock } from './context';
import { opsHealthKey } from './queries';

export function OpsGate({ children }: { children: ReactNode }) {
  const lock = useOpsLock();
  const queryClient = useQueryClient();
  // Nothing an operator saw stays in memory after a lock.
  useEffect(() => {
    if (lock.unlocked) return;
    queryClient.removeQueries({ queryKey: ['ops'] });
    const mutations = queryClient.getMutationCache();
    mutations.findAll({ mutationKey: ['ops'] }).forEach((m) => mutations.remove(m));
  }, [lock.unlocked, queryClient]);
  return lock.unlocked ? children : <UnlockScreen reason={lock.reason} />;
}

function UnlockScreen({ reason }: { reason: 'locked' | 'idle' | 'rejected' | null }) {
  const { store, client } = useOps();
  const queryClient = useQueryClient();
  const [key, setKey] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const field = useRef<HTMLInputElement>(null);

  useEffect(() => {
    document.title = en.ops.title;
  }, []);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    const candidate = key.trim();
    if (!candidate) {
      setError(en.ops.unlock.required);
      field.current?.focus();
      return;
    }
    setError(null);
    setFormError(null);
    setPending(true);
    try {
      const health = unwrap(await client.GET('/internal/v1/health/ops', { headers: { [OPS_KEY_HEADER]: candidate } }));
      queryClient.setQueryData(opsHealthKey, health);
      store.set(candidate);
    } catch (err) {
      setPending(false);
      if (isApiError(err) && err.status === 401) {
        setError(en.ops.unlock.wrongKey);
        field.current?.focus();
      } else if (isApiError(err) && err.status === 404) setFormError(en.ops.unlock.notEnabled);
      else setFormError(messageForError(err));
    }
  };

  const notice = reason ? en.ops.unlock[reason] : null;
  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center px-4 py-10">
      <h1 className="mb-2 text-2xl font-semibold tracking-tight">{en.ops.unlock.title}</h1>
      <p className="mb-6 text-sm text-slate-600">{en.ops.unlock.intro}</p>
      <form noValidate onSubmit={(e) => void onSubmit(e)} className="space-y-4 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
        {notice && !formError && <Alert tone={reason === 'rejected' ? 'error' : 'info'}>{notice}</Alert>}
        {formError && <Alert tone="error">{formError}</Alert>}
        <PasswordField
          ref={field}
          label={en.ops.unlock.key}
          name="operator-key"
          autoComplete="off"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          error={error}
        />
        <Button loading={pending}>{en.ops.unlock.submit}</Button>
      </form>
    </main>
  );
}
