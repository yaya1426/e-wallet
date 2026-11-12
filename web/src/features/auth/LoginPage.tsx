import { useRef, useState, type FormEvent } from 'react';
import { Link, useLocation } from 'react-router';
import { useAuth } from '@/auth/AuthProvider';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { PageLayout } from '@/components/PageLayout';
import { PasswordField } from '@/components/PasswordField';
import { TextField } from '@/components/TextField';
import { en } from '@/i18n/en';
import { focusFirstInvalid } from '@/lib/focus';
import { normalizeEgyptianMobile } from '@/lib/phone';
import { useSubmit } from '@/lib/useSubmit';
import { endReasonNotice, type LoginLocationState } from './notices';

export function LoginPage() {
  const { session, state } = useAuth();
  const location = useLocation();
  const routed = (location.state ?? {}) as LoginLocationState;
  const [phone, setPhone] = useState(routed.phone ?? '');
  const [password, setPassword] = useState('');
  const [errors, setErrors] = useState<{ phone?: string; password?: string }>({});
  const { pending, formError, run } = useSubmit();
  const formRef = useRef<HTMLFormElement>(null);

  const notice = routed.notice
    ? { tone: routed.notice === 'passwordChanged' ? ('success' as const) : ('info' as const), text: en.notices[routed.notice] }
    : endReasonNotice(state.endReason);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const normalized = normalizeEgyptianMobile(phone);
    const next = {
      phone: normalized ? undefined : en.validation.phoneInvalid,
      password: password ? undefined : en.validation.required,
    };
    setErrors(next);
    if (!normalized || next.password) {
      focusFirstInvalid(formRef.current);
      return;
    }
    // On success the session becomes authenticated and RedirectIfAuthed moves on (PRD-005 UI-BR-8).
    await run(() => session.login(normalized, password));
  }

  return (
    <PageLayout title={en.login.title}>
      <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-4">
        {notice && !formError && <Alert tone={notice.tone}>{notice.text}</Alert>}
        {state.endReason === 'network' && !formError && (
          <Button type="button" variant="secondary" onClick={() => void session.restore()}>
            {en.common.retry}
          </Button>
        )}
        {formError && <Alert tone="error">{formError}</Alert>}
        <TextField
          label={en.fields.phone}
          hint={en.fields.phoneHint}
          name="phone"
          type="tel"
          inputMode="tel"
          autoComplete="tel"
          value={phone}
          onChange={(e) => setPhone(e.target.value)}
          error={errors.phone}
        />
        <PasswordField
          label={en.fields.password}
          name="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          error={errors.password}
        />
        <Button loading={pending}>{en.login.submit}</Button>
      </form>
      <div className="mt-6 space-y-2 text-center text-sm text-slate-600">
        <p>
          <Link className="font-medium text-brand-600 hover:underline" to="/reset-password">
            {en.login.forgot}
          </Link>
        </p>
        <p>
          {en.login.noAccount}{' '}
          <Link className="font-medium text-brand-600 hover:underline" to="/register">
            {en.login.toRegister}
          </Link>
        </p>
      </div>
    </PageLayout>
  );
}
