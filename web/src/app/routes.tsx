import type { RouteObject } from 'react-router';
import { RedirectIfAuthed, RequireAuth } from '@/auth/guards';
import { LoginPage } from '@/features/auth/LoginPage';
import { RegisterPage } from '@/features/auth/RegisterPage';
import { ResetPasswordPage } from '@/features/auth/ResetPasswordPage';
import { HomePlaceholder } from '@/features/home/HomePlaceholder';
import { NotFoundPage } from '@/features/NotFoundPage';

export const routes: RouteObject[] = [
  { path: '/login', element: <RedirectIfAuthed><LoginPage /></RedirectIfAuthed> },
  { path: '/register', element: <RedirectIfAuthed><RegisterPage /></RedirectIfAuthed> },
  { path: '/reset-password', element: <RedirectIfAuthed><ResetPasswordPage /></RedirectIfAuthed> },
  { path: '/', element: <RequireAuth><HomePlaceholder /></RequireAuth> },
  { path: '*', element: <NotFoundPage /> },
];
