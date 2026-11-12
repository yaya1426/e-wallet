// The console's credential and client, provided to every ops page (TDR-022).
import { createContext, useContext, useSyncExternalStore, type ReactNode } from 'react';
import type { OpsClient } from '@/api/opsClient';
import type { OpsKeyStore } from './opsKey';

interface Ops {
  store: OpsKeyStore;
  client: OpsClient;
}

const OpsContext = createContext<Ops | null>(null);

export function OpsProvider({ store, client, children }: Ops & { children: ReactNode }) {
  return <OpsContext.Provider value={{ store, client }}>{children}</OpsContext.Provider>;
}

export function useOps(): Ops {
  const ops = useContext(OpsContext);
  if (!ops) throw new Error('useOps outside OpsProvider');
  return ops;
}

export function useOpsLock() {
  const { store } = useOps();
  return useSyncExternalStore(store.subscribe, store.getSnapshot);
}
