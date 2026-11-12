// The operator console (PRD-005 area 5, TDR-022): a lazily loaded route tree under /ops with its
// own credential. It never reads or changes the user session.
import { useEffect, useState } from 'react';
import { Route, Routes } from 'react-router';
import { createOpsClient } from '@/api/opsClient';
import { OpsProvider } from './context';
import { DiscrepanciesPage } from './DiscrepanciesPage';
import { DiscrepancyDetailPage } from './DiscrepancyDetailPage';
import { OpsGate } from './OpsGate';
import { OpsHealthPage } from './OpsHealthPage';
import { OpsLayout, OpsNotFound } from './OpsLayout';
import { browserOpsKeyDeps, OpsKeyStore } from './opsKey';
import { ResetCodePage } from './ResetCodePage';
import { WalletsPage } from './WalletsPage';

export default function OpsRoutes() {
  const [ops] = useState(() => {
    const store = new OpsKeyStore(browserOpsKeyDeps());
    return { store, client: createOpsClient(store, (req) => globalThis.fetch(req), window.location.origin) };
  });
  // The idle timer keeps running after leaving /ops, so the key is removed on time (TDR-022 §3).
  useEffect(() => ops.store.start(), [ops]);

  // Keep the console out of search engines.
  useEffect(() => {
    const meta = document.createElement('meta');
    meta.name = 'robots';
    meta.content = 'noindex';
    document.head.appendChild(meta);
    return () => meta.remove();
  }, []);

  return (
    <OpsProvider store={ops.store} client={ops.client}>
      <OpsGate>
        <Routes>
          <Route element={<OpsLayout />}>
            <Route index element={<OpsHealthPage />} />
            <Route path="discrepancies" element={<DiscrepanciesPage />} />
            <Route path="discrepancies/:id" element={<DiscrepancyDetailPage />} />
            <Route path="wallets" element={<WalletsPage />} />
            <Route path="reset-codes" element={<ResetCodePage />} />
            <Route path="*" element={<OpsNotFound />} />
          </Route>
        </Routes>
      </OpsGate>
    </OpsProvider>
  );
}
