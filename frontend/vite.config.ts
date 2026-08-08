import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // Emit .map files alongside the JS bundles but do NOT add a
    // sourceMappingURL comment to the built artifacts. Production
    // users see leaner bundles while operators still get stack
    // traces when reverse-mapping errors in a debugger.
    sourcemap: 'hidden',
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
  },
})
