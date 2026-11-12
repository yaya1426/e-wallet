import type { RouteObject } from 'react-router';
import { RedirectIfAuthed, RequireAuth } from '@/auth/guards';
import { AppShell } from '@/components/AppShell';
import { LoginPage } from '@/features/auth/LoginPage';
import { RegisterPage } from '@/features/auth/RegisterPage';
import { ResetPasswordPage } from '@/features/auth/ResetPasswordPage';
import { HistoryPage } from '@/features/home/HistoryPage';
import { BankAccountPage } from '@/features/money/BankAccountPage';
import { TopUpPage, WithdrawPage } from '@/features/money/MoneyFlowPage';
import { HomePage } from '@/features/home/HomePage';
import { TransactionDetailPage } from '@/features/home/TransactionDetailPage';
import { NotFoundPage } from '@/features/NotFoundPage';
import { en } from '@/i18n/en';
import { ComingSoonPage } from './ComingSoonPage';

export const routes: RouteObject[] = [
  { path: '/login', element: <RedirectIfAuthed><LoginPage /></RedirectIfAuthed> },
  { path: '/register', element: <RedirectIfAuthed><RegisterPage /></RedirectIfAuthed> },
  { path: '/reset-password', element: <RedirectIfAuthed><ResetPasswordPage /></RedirectIfAuthed> },
  {
    element: <RequireAuth><AppShell /></RequireAuth>,
    children: [
      { path: '/', element: <HomePage /> },
      { path: '/history', element: <HistoryPage /> },
      { path: '/transactions/:id', element: <TransactionDetailPage /> },
      { path: '/bank-account', element: <BankAccountPage /> },
      { path: '/top-up', element: <TopUpPage /> },
      { path: '/withdraw', element: <WithdrawPage /> },
      { path: '/send', element: <ComingSoonPage title={en.nav.send} /> },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
];
