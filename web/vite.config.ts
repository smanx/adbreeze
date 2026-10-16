import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// base './'：产物由 Go go:embed 嵌入后可从任意路径服务
// dev 代理：本地开发时直连本机运行的 ADBreeze 服务
export default defineConfig({
  base: './',
  plugins: [react()],
  server: {
    proxy: {
      '/api': 'http://localhost:9080',
      '/ws': { target: 'ws://localhost:9080', ws: true },
    },
  },
  build: { outDir: 'dist' },
})
