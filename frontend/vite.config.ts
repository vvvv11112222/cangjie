import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';

// M0 前端骨架不连后端：页面直接读取仓库内 contracts/examples 的固定样例。
// 正式部署由 Go 同源提供静态文件，因此使用相对 base；/api 代理是 M1 的接入点。
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
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
});
