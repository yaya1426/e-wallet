// Placeholder for routes whose screens land in later tasks (T032, T033).
import { Card, Page } from '@/components/AppShell';
import { en } from '@/i18n/en';

export function ComingSoonPage({ title }: { title: string }) {
  return (
    <Page title={title}>
      <Card>
        <p className="text-slate-600">{en.nav.comingSoonBody}</p>
      </Card>
    </Page>
  );
}
