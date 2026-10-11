import { defineConfig } from "@playwright/test"

const API_PORT = Number(process.env.E2E_API_PORT ?? 18080)
const WEB_PORT = Number(process.env.E2E_WEB_PORT ?? 18300)
const CONTROL_PORT = Number(process.env.E2E_CONTROL_PORT ?? 18480)

export default defineConfig({
  testDir: "./e2e",
  timeout: 90_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"]],
  use: {
    actionTimeout: 15_000,
    baseURL: `http://localhost:${WEB_PORT}`,
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: `API_PORT=${API_PORT} REGISTER_RATE_LIMIT=100 LOGIN_RATE_LIMIT=100 CORS_ORIGINS=http://localhost:${WEB_PORT},http://127.0.0.1:${WEB_PORT} PUBLIC_BASE_URL=http://localhost:${WEB_PORT} make -C .. api-dev`,
      url: `http://127.0.0.1:${API_PORT}/health/live`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      // Host CPU load is unrelated to the application faults under test.
      command: `LOAD_THRESHOLD=100 DEMO_LISTEN_ADDR=127.0.0.1:${CONTROL_PORT} BACKEND_A_LISTEN_ADDR=127.0.0.1:${CONTROL_PORT + 1} BACKEND_B_LISTEN_ADDR=127.0.0.1:${CONTROL_PORT + 2} DEMO_PROBE_INTERVAL_MS=1000 go run ../cmd/demo`,
      url: `http://127.0.0.1:${CONTROL_PORT}/health/live`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command: `API_ORIGIN=http://127.0.0.1:${API_PORT} CONTINUUM_CONTROL_ORIGIN=http://127.0.0.1:${CONTROL_PORT} CONTINUUM_DEMO=1 bun run dev --port ${WEB_PORT}`,
      url: `http://localhost:${WEB_PORT}`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
})
