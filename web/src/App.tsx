import { useEffect } from 'react'
import { loadHealth, useStore } from './store'
import Shell from './components/Shell'

// 登录由后端 Basic Auth 弹窗完成：页面本身受保护，
// 能加载到这里说明已通过认证，无需再渲染前端登录门。
export default function App() {
  useEffect(() => {
    loadHealth().then(h => {
      if (h?.version) useStore.setState({ version: h.version })
    })
  }, [])

  return <Shell />
}
