import { defineConfig } from '@playwright/test'

// End-to-end tests drive the real server with a throwaway data directory: `npm run e2e` builds the UI (it is
// embedded in the binary), starts RepoMan on :18082 and runs the specs in ./e2e against it.
// Uses the machine's Chrome (E2E_CHANNEL=msedge for Edge); set E2E_CHANNEL= to use Playwright's own Chromium
// after `npx playwright install chromium`.
const port = Number(process.env.E2E_PORT ?? 18082)

export default defineConfig({
  testDir: './e2e',
  timeout: 45_000,
  workers: 1, // the specs share one server and seed data
  fullyParallel: false,
  reporter: [['list']],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    channel: process.env.E2E_CHANNEL ?? 'chrome',
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'go -C ../backend run .',
    url: `http://127.0.0.1:${port}/healthz`,
    reuseExistingServer: !!process.env.E2E_REUSE,
    timeout: 180_000,
    env: {
      RM_ADDR: `127.0.0.1:${port}`,
      RM_DATA_DIR: `${process.cwd()}/.e2e-data`,
      RM_ADMIN_PASSWORD: 'e2e-admin-pw',
      RM_ACCESS_LOG: 'false',
    },
  },
})
