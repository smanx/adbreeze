import { useState } from 'react'
import { useStore } from '../store'

// 鉴权页：输入服务启动时打印的 API Token，存 localStorage
export default function AuthGate() {
  const setToken = useStore(s => s.setToken)
  const [value, setValue] = useState('')
  const [error, setError] = useState('')

  const save = () => {
    const t = value.trim()
    if (!t) { setError('请输入 Token'); return }
    setToken(t)
  }

  return (
    <div className="auth-page">
      <div className="auth-card">
        <h1>ADBreeze</h1>
        <p className="auth-sub">跨平台 ADB 投屏与控制服务</p>
        <input
          className="auth-input"
          placeholder="API Token（服务启动时打印）"
          value={value}
          onChange={e => { setValue(e.target.value); setError('') }}
          onKeyDown={e => e.key === 'Enter' && save()}
          autoFocus
        />
        {error && <p className="auth-error">{error}</p>}
        <button className="btn primary" onClick={save}>进入控制台</button>
      </div>
    </div>
  )
}
