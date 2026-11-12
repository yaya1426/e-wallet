import { Link } from 'react-router';
import { PageLayout } from '@/components/PageLayout';
import { en } from '@/i18n/en';

export function NotFoundPage() {
  return (
    <PageLayout title={en.notFound.title}>
      <p className="mb-6 text-slate-600">{en.notFound.body}</p>
      <Link className="font-medium text-brand-600 hover:underline" to="/">
        {en.common.goHome}
      </Link>
    </PageLayout>
  );
}
