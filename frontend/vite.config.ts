import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Build output is embedded into the Go binary; dev server proxies to the Go backend.
const backend = 'http://localhost:8081'

export default defineConfig({
  plugins: [react()],
  // cssTarget: older targets make Lightning CSS polyfill light-dark() with variables Mantine never sets,
  // which silently breaks every themed rule in app.css. These are the first versions with native light-dark().
  build: { outDir: '../backend/ui/dist', emptyOutDir: true, chunkSizeWarningLimit: 1000, cssTarget: ['chrome123', 'edge123', 'firefox120', 'safari17.5'] },
  server: { proxy: { '/api': backend, '/repository': backend, '/v2': backend } },
})
