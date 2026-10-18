import { useEffect, useState } from 'react'
import { loadHealth, useStore } from './store'
import Shell from './components/Shell'
import AboutPage from './components/AboutPage'

// 登录由页面内的登录表单完成（未持有效会话时 Shell 整屏切换到登录卡片），
// 能渲染到应用外壳说明已通过 /api/session 鉴权。
//
// /about 是独立的介绍页路由（无需登录、不加载控制台）：路径以 /about 结尾即渲染
// （endsWith 兼容反代/子路径部署），「进入控制台」跳回根路径。
// 双语偏好共用同一 i18n 模块（localStorage），两页自动同步。
export default function App() {
  const [about, setAbout] = useState(() => isAbout())

  useEffect(() => {
    const onPop = () => setAbout(isAbout())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
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
  return location.pathname.replace(/\/+$/, '').endsWith('/about')
}
