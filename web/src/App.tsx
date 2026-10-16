import { useEffect } from 'react'
import { loadHealth, useStore } from './store'
import Shell from './components/Shell'

// 登录由页面内的登录表单完成（未持有效会话时 Shell 整屏切换到登录卡片），
// 能渲染到应用外壳说明已通过 /api/session 鉴权。
export default function App() {
  useEffect(() => {
    loadHealth().then(h => {
      if (h?.version) useStore.setState({ version: h.version })
    })
  }, [])

  return <Shell />
}
