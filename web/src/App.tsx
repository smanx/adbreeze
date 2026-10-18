import { useEffect, useState } from 'react'
import { loadHealth, useStore } from './store'
import Shell from './components/Shell'
import AboutPage from './components/AboutPage'

// 登录由页面内的登录表单完成（未持有效会话时 Shell 整屏切换到登录卡片），
// 能渲染到应用外壳说明已通过 /api/session 鉴权。
//
// 介绍页走 hash 路由 /#/about：GitHub Pages 等纯静态托管没有 SPA fallback，
// 路径式路由（/about）刷新必 404；hash 完全由前端解析，任何托管都能用。
// 自托管（Go embed）下两种写法都工作。双语偏好共用同一 i18n 模块，两页自动同步。
export default function App() {
  const [about, setAbout] = useState(() => isAbout())

  // hashchange 覆盖前进/后退与手动改地址；popstate 在 hash 导航时也会触发，
  // 只听一个事件足够
  useEffect(() => {
    const sync = () => setAbout(isAbout())
    window.addEventListener('hashchange', sync)
    return () => window.removeEventListener('hashchange', sync)
  }, [])

  useEffect(() => {
    if (about) return
    loadHealth().then(h => {
      if (h?.version) useStore.setState({ version: h.version })
    })
  }, [about])

  return about ? <AboutPage /> : <Shell />
}

function isAbout(): boolean {
  return location.hash.replace(/\/+$/, '').endsWith('#/about')
}
