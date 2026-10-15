import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './styles.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

// 注册 Service Worker 以支持 PWA 安装与离线打开外壳。
// 生产构建才会产出稳定资源名；dev 下 vite 会注入额外的 HMR 脚本，
// 且 SW 缓存会干扰热更新，因此仅在非 dev 环境注册。
// 注意：WebCodecs 等能力依赖安全上下文（HTTPS/localhost），SW 本身在 HTTP+IP 下也能注册，
// 但只有安全上下文才会被视为「可安装」的 PWA。
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('./sw.js').catch(() => {
      // 注册失败（如非安全上下文）不影响应用本体，静默忽略
    })
  })
}
