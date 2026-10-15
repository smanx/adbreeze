import { useEffect } from 'react'
import { loadHealth, useStore } from './store'
import AuthGate from './components/AuthGate'
import Shell from './components/Shell'

export default function App() {
  const token = useStore(s => s.token)

  useEffect(() => {
    loadHealth().then(h => {
      if (h?.version) useStore.setState({ version: h.version })
    })
  }, [])

  if (!token) return <AuthGate />
  return <Shell />
}
