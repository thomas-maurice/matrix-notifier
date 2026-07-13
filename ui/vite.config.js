import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    // `npm run dev` against a locally running bot.
    proxy: {
      '/notifier.v1.AdminService': 'http://localhost:8686',
    },
  },
})
