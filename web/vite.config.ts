import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// base './'：产物由 Go go:embed 嵌入后可从任意路径服务
// dev 代理：本地开发时直连本机运行的 ADBreeze 服务
//
// 两种构建模式：
// - 默认（主应用，go:embed / Docker 用）：不含 about 截图
// - --mode site（Nginx / GitHub Pages 用）：额外拷贝 screenshots/ 到 dist-site/
export default defineConfig(({ mode }) => {
  const isSite = mode === 'site'
  return {
    base: './',
    plugins: [react()],
    server: {
      proxy: {
        '/api': 'http://localhost:9080',
        '/ws': { target: 'ws://localhost:9080', ws: true },
      },
    },
    build: {
      outDir: isSite ? 'dist-site' : 'dist',
    },
  }
})
