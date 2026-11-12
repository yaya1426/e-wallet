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
import { validateConfirmation, validateFullName, validatePassword } from '@/lib/validation';
import type { LoginLocationState } from './notices';

type Field = 'phone' | 'fullName' | 'password' | 'confirm';
type FieldErrors = Partial<Record<Field, string>>;

/** API codes that belong to one field (PRD-005 UI-BR-7). */
const fieldOfCode: Record<string, Field> = {
  INVALID_PHONE: 'phone',
  PHONE_ALREADY_REGISTERED: 'phone',
  INVALID_NAME: 'fullName',
  INVALID_PASSWORD: 'password',
};

export function RegisterPage() {
  const { session } = useAuth();
  const api = useApi();
  const navigate = useNavigate();
  const [phone, setPhone] = useState('');
  const [fullName, setFullName] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [duplicate, setDuplicate] = useState(false);
  const { pending, formError, run } = useSubmit();
  const formRef = useRef<HTMLFormElement>(null);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setDuplicate(false);
    const normalized = normalizeEgyptianMobile(phone);
    const v = (key: keyof typeof en.validation | null) => (key ? en.validation[key] : undefined);
    const next: FieldErrors = {
      phone: normalized ? undefined : en.validation.phoneInvalid,
      fullName: v(validateFullName(fullName)),
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
          await api.POST('/v1/auth/register', {
            body: { phone: normalized, full_name: fullName.trim(), password },
          }),
        );
        try {
          // Register returns no token: log in with the same credentials (UI-BR-7).
          await session.login(normalized, password);
        } catch {
          const state: LoginLocationState = { phone: normalized, notice: 'accountCreated' };
          navigate('/login', { state });
        }
      },
      (err) => {
        const field = fieldOfCode[err.code];
        if (!field) return false;
        setErrors((prev) => ({ ...prev, [field]: en.errors[err.code] }));
        setDuplicate(err.code === 'PHONE_ALREADY_REGISTERED');
        focusFirstInvalid(formRef.current);
        return true;
      },
    );
  }

  return (
    <PageLayout title={en.register.title}>
      <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-4">
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
        {duplicate && (
          <p className="-mt-2 text-sm">
            <Link className="font-medium text-brand-600 hover:underline" to="/login" state={{ phone: normalizeEgyptianMobile(phone) ?? phone }}>
              {en.register.loginInstead}
            </Link>
          </p>
        )}
        <TextField
          label={en.fields.fullName}
          name="full_name"
          autoComplete="name"
          value={fullName}
          onChange={(e) => setFullName(e.target.value)}
          error={errors.fullName}
        />
        <PasswordField
          label={en.fields.password}
          hint={en.fields.passwordHint}
          name="password"
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
        <Button loading={pending}>{en.register.submit}</Button>
      </form>
      <p className="mt-6 text-center text-sm text-slate-600">
        {en.register.haveAccount}{' '}
        <Link className="font-medium text-brand-600 hover:underline" to="/login">
          {en.register.toLogin}
        </Link>
      </p>
    </PageLayout>
  );
}
