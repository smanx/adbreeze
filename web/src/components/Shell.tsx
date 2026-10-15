import { useEffect, useState } from 'react'
import { clearReconnectTimer, useStore } from '../store'
import { api, clearSession, type AdbSettings, type AdbSettingsSaveResult } from '../api'
import { Placeholder, TABS } from '../panels'
import DeviceList from './DeviceList'
import MirrorPanel from '../panels/MirrorPanel'
import FilesPanel from '../panels/FilesPanel'
import TermPanel from '../panels/TermPanel'
import ToolsPanel from '../panels/ToolsPanel'
import OverviewPanel from '../panels/OverviewPanel'
import LogcatPanel from '../panels/LogcatPanel'
import AppsPanel from '../panels/AppsPanel'
import ProcessPanel from '../panels/ProcessPanel'
import PerfPanel from '../panels/PerfPanel'
import LayoutPanel from '../panels/LayoutPanel'

// 应用外壳：
//   PC（≥880px）：顶栏 + 左侧设备栏 + 主内容（标签页面板）
//   手机（<880px）：顶栏 + 设备抽屉 + 主内容 + 底部 TabBar
export default function Shell() {
  const panel = useStore(s => s.panel)
  const setPanel = useStore(s => s.setPanel)
  const selected = useStore(s => s.selected)
  const serverConnected = useStore(s => s.serverConnected)
  const version = useStore(s => s.version)
  const watchEvents = useStore(s => s.watchEvents)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [loggedOut, setLoggedOut] = useState(false)

  useEffect(() => {
    if (loggedOut) return
    watchEvents()
    return () => { clearReconnectTimer() }
  }, [loggedOut])

  // 退出：清除后端会话 cookie 并断开事件通道。
  // Basic 凭证由浏览器按源缓存、无法由脚本清除，故此处仅做降级退出提示。
  const logout = async () => {
    if (!confirm('退出将结束当前会话（浏览器记住的登录凭证需关闭标签页后才会失效），确定？')) return
    clearReconnectTimer()
    await clearSession()
    setLoggedOut(true)
  }

  if (loggedOut) {
    return (
      <div className="auth-page">
        <div className="auth-card">
          <h1>已退出</h1>
          <p className="auth-sub">会话已结束。关闭此标签页即可彻底清除浏览器保存的登录凭证。</p>
          <button className="btn primary" onClick={() => location.reload()}>重新登录</button>
        </div>
      </div>
    )
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <button className="icon-btn drawer-toggle" onClick={() => setDrawerOpen(true)}>☰</button>
        <span className="brand">ADBreeze</span>
        {version && <span className="version-badge">v{version}</span>}
        <span className={'conn-dot' + (serverConnected ? ' ok' : '')} title={serverConnected ? '服务在线' : '重连中'} />
        <span className="current-device">{selected || '未选择设备'}</span>
        <span className="spacer" />
        <nav className="topbar-tabs">
          {TABS.map(t => (
            <button
              key={t.id}
              className={'tab' + (panel === t.id ? ' active' : '')}
              onClick={() => setPanel(t.id)}
            >{t.label}</button>
          ))}
        </nav>
        <a className="btn ghost" href="./legacy.html" target="_blank" rel="noreferrer">旧版页面</a>
        <button className="btn ghost" onClick={logout}>退出</button>
      </header>

      <div className="app-body">
        <aside className="sidebar">
          <h3 className="side-title">设备</h3>
          <DeviceList />
        </aside>

        <main className="content">
          {panel === 'overview' ? <OverviewPanel />
            : panel === 'mirror' ? <MirrorPanel />
            : panel === 'files' ? <FilesPanel />
            : panel === 'apps' ? <AppsPanel />
            : panel === 'process' ? <ProcessPanel />
            : panel === 'perf' ? <PerfPanel />
            : panel === 'layout' ? <LayoutPanel />
            : panel === 'term' ? <TermPanel />
            : panel === 'tools' ? <ToolsPanel />
            : panel === 'logcat' ? <LogcatPanel />
            : panel === 'settings' ? <SettingsPanel />
            : <Placeholder title={TABS.find(t => t.id === panel)!.label}
                phase={TABS.find(t => t.id === panel)!.phase || '后续阶段'} selected={selected} />}
        </main>
      </div>

      {drawerOpen && (
        <>
          <div className="drawer-mask" onClick={() => setDrawerOpen(false)} />
          <aside className="drawer">
            <h3 className="side-title">设备</h3>
            <DeviceList />
          </aside>
        </>
      )}

      <nav className="tabbar">
        {TABS.map(t => (
          <button
            key={t.id}
            className={'tab' + (panel === t.id ? ' active' : '')}
            onClick={() => setPanel(t.id)}
          >{t.label}</button>
        ))}
      </nav>
    </div>
  )
}

function SettingsPanel() {
  const [info, setInfo] = useState<AdbSettings | null>(null)
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')

  const load = async () => {
    try {
      const r = await api<AdbSettings>('GET', '/api/settings/adb')
      setInfo(r)
      setDraft(r.path)
      setErr('')
    } catch (e) {
      setErr('读取 ADB 设置失败: ' + (e as Error).message)
    }
  }
  useEffect(() => { void load() }, [])

  // 保存并立即生效：后端会用新路径执行 adb version 验证，再重启 adb server
  const save = async (path: string) => {
    setBusy(true); setErr(''); setMsg('')
    try {
      const r = await api<AdbSettingsSaveResult>('POST', '/api/settings/adb', { path })
      setMsg(r.message + (r.version ? '（' + r.version.split('\n')[0] + '）' : ''))
      await load()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const usingDefault = !!info && info.path === ''

  return (
    <div className="panel settings-panel">
      <h2>设置</h2>

      <div className="settings-block">
        <h3>ADB 路径</h3>
        <p className="hint">
          后端只在「拉起 / 重启 adb server」时用到 adb 可执行文件；
          切换后会立即用新路径重启一次 server 使其生效。可留空使用默认（从 PATH 查找）。
        </p>
        <div className="adb-path-row">
          <input
            type="text"
            value={draft}
            placeholder={info?.available ? '留空使用默认：' + info.default : '未在 PATH 中找到 adb，请填写完整路径'}
            onChange={e => { setDraft(e.target.value); setErr(''); setMsg('') }}
            onKeyDown={e => e.key === 'Enter' && !busy && save(draft.trim())}
            disabled={busy}
          />
          <button className="btn primary" disabled={busy || usingDefault && draft.trim() === ''}
            onClick={() => save(draft.trim())}>
            {busy ? '切换中…' : '保存并切换'}
          </button>
          <button className="btn" disabled={busy || usingDefault}
            onClick={() => save('')} title="改回从 PATH 查找">
            恢复默认
          </button>
        </div>
        {err && <p className="hint error-text">{err}</p>}
        {msg && <p className="hint ok-text">{msg}</p>}
        {info && (
          <p className="hint">
            当前生效：<code>{info.path || '默认（' + (info.available ? info.default : 'PATH 中未找到 adb') + '）'}</code>
            {'　'}存档：<code>{info.configFile || '（未启用持久化）'}</code>
          </p>
        )}
      </div>

      <p className="hint">
        登录使用浏览器自带的 Basic Auth（账号密码由服务启动时配置，浏览器按站点缓存）。
        修改密码需重启服务并指定 -password；切换账号或彻底退出需关闭标签页 / 清除浏览器站点数据。
        投屏画质/声音等设置在投屏面板内按设备单独保存。
      </p>
    </div>
  )
}
