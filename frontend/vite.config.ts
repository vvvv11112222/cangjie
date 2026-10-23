import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';

// Static assets use a relative base; the website gateway proxies API requests.
export default defineConfig({
  base: './',
  plugins: [react()],
  build: {
    rollupOptions: {
      input: {
        main: fileURLToPath(new URL('./index.html', import.meta.url)),
        phone: fileURLToPath(new URL('./capture-phone.html', import.meta.url)),
      },
    },
  },
  server: {
    fs: {
      // 样例位于仓库根目录的 contracts/，需要允许 Vite 读取上级目录。
      allow: ['..'],
    },
    proxy: {
      '/api': {
        target: process.env.TEAM_API_TARGET ?? 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
  preview: { proxy: { '/api': { target: process.env.TEAM_API_TARGET ?? 'http://127.0.0.1:8080', changeOrigin: false }, '/health': { target: process.env.TEAM_API_TARGET ?? 'http://127.0.0.1:8080', changeOrigin: false } } },
});
