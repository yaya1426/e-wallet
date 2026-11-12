import { useRef, useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router';
import { unwrap } from '@/api/client';
import { useApi, useAuth } from '@/auth/AuthProvider';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { PageLayout } from '@/components/PageLayout';
import { PasswordField } from '@/components/PasswordField';
import { TextField } from '@/components/TextField';
import { en } from '@/i18n/en';
import { focusFirstInvalid } from '@/lib/focus';
import { normalizeEgyptianMobile } from '@/lib/phone';
import { useSubmit } from '@/lib/useSubmit';
import { validateConfirmation, validatePassword, validateResetCode } from '@/lib/validation';
import type { LoginLocationState } from './notices';

type Field = 'phone' | 'code' | 'password' | 'confirm';

/** Operator-assisted reset (PRD-005 UI-BR-10, TDR-018 §4). */
export function ResetPasswordPage() {
  const { session } = useAuth();
  const api = useApi();
  const navigate = useNavigate();
  const [phone, setPhone] = useState('');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const { pending, formError, run } = useSubmit();
  const formRef = useRef<HTMLFormElement>(null);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const normalized = normalizeEgyptianMobile(phone);
    const v = (key: keyof typeof en.validation | null) => (key ? en.validation[key] : undefined);
    const next = {
      phone: normalized ? undefined : en.validation.phoneInvalid,
      code: v(validateResetCode(code)),
      password: v(validatePassword(password)),
      confirm: v(validateConfirmation(password, confirm)),
    };
    setErrors(next);
    if (!normalized || Object.values(next).some(Boolean)) {
      focusFirstInvalid(formRef.current);
      return;
    }
    await run(
      async () => {
        unwrap(
          await api.POST('/v1/auth/password-reset', {
            body: { phone: normalized, code: code.trim(), new_password: password },
          }),
        );
        // The server revoked every session of this user; drop any local one too.
        session.end('signedOut');
        const state: LoginLocationState = { phone: normalized, notice: 'passwordChanged' };
        navigate('/login', { state, replace: true });
      },
      (err) => {
        if (err.code !== 'INVALID_PASSWORD') return false;
        setErrors((prev) => ({ ...prev, password: en.errors.INVALID_PASSWORD }));
        return true;
      },
    );
  }

  return (
    <PageLayout title={en.reset.title}>
      <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-4">
        <Alert tone="info">{en.reset.explain}</Alert>
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
        <TextField
          label={en.fields.resetCode}
          hint={en.fields.resetCodeHint}
          name="code"
          inputMode="numeric"
          autoComplete="one-time-code"
          value={code}
          onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
          error={errors.code}
        />
        <PasswordField
          label={en.fields.newPassword}
          hint={en.fields.passwordHint}
          name="new_password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          error={errors.password}
        />
        <PasswordField
          label={en.fields.confirmPassword}
          name="confirm_password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          error={errors.confirm}
        />
        <Button loading={pending}>{en.reset.submit}</Button>
      </form>
      <p className="mt-6 text-center text-sm">
        <Link className="font-medium text-brand-600 hover:underline" to="/login">
          {en.reset.backToLogin}
        </Link>
      </p>
    </PageLayout>
  );
}
