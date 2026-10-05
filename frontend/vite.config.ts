import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// M0 前端骨架不连后端：页面直接读取仓库内 contracts/examples 的固定样例。
// 正式部署由 Go 同源提供静态文件，因此使用相对 base；/api 代理是 M1 的接入点。
export default defineConfig({
  base: './',
  plugins: [react()],
  server: {
    fs: {
      // 样例位于仓库根目录的 contracts/，需要允许 Vite 读取上级目录。
      allow: ['..'],
    },
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
});
