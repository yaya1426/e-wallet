import { defineConfig, devices } from '@playwright/test';

// End-to-end tests (T030). By default they build the app, serve it with `vite preview` (which
// proxies the API to WALLET_API_URL, default the compose wallet on :8080) and run against it.
// Set WEB_BASE_URL to test an already deployed app instead (only the smoke spec is safe there).
const deployed = process.env.WEB_BASE_URL;
const baseURL = deployed ?? 'http://localhost:4173';

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: { baseURL, trace: 'retain-on-failure' },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
    // The login limit is 30 attempts per client IP per minute (T004) and every test here comes
    // from one IP, so the phone layout runs only the tests tagged @mobile plus the smoke tests.
    { name: 'mobile', use: { ...devices['Pixel 7'], viewport: { width: 390, height: 844 } }, grep: /@mobile|smoke/ },
  ],
  webServer: deployed
    ? undefined
    : {
        command: 'npm run build && npm run preview -- --port 4173 --strictPort',
        url: 'http://localhost:4173',
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      },
});
