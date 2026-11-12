import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState } from 'react';
import { RouterProvider, type createBrowserRouter } from 'react-router';
import type { ApiClient } from '@/api/client';
import { isApiError } from '@/api/errors';
import { AuthProvider } from '@/auth/AuthProvider';
import type { Session } from '@/auth/session';

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Retry once on network/5xx, never on a 4xx (the answer won't change).
        retry: (failureCount, error) =>
          failureCount < 1 && !(isApiError(error) && error.status >= 400 && error.status < 500),
        refetchOnWindowFocus: false,
      },
      mutations: { retry: 0 },
    },
  });
}

interface Props {
  session: Session;
  api: ApiClient;
  router: ReturnType<typeof createBrowserRouter>;
  queryClient?: QueryClient;
}

export function App({ session, api, router, queryClient }: Props) {
  const [client] = useState(() => queryClient ?? createQueryClient());
  return (
    <QueryClientProvider client={client}>
      <AuthProvider session={session} api={api}>
        <RouterProvider router={router} />
      </AuthProvider>
    </QueryClientProvider>
  );
}
