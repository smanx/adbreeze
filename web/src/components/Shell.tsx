import { useEffect, useState } from 'react'
import { clearReconnectTimer, useStore } from '../store'
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
  const setToken = useStore(s => s.setToken)
  const [drawerOpen, setDrawerOpen] = useState(false)

  useEffect(() => {
    watchEvents()
    return () => { clearReconnectTimer() }
  }, [])

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
        <button className="btn ghost" onClick={() => { if (confirm('退出将清除 Token，确定？')) { localStorage.removeItem('ADBreeze-token'); setToken(''); clearReconnectTimer() } }}>退出</button>
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
  const token = useStore(s => s.token)
  const setToken = useStore(s => s.setToken)
  const [value, setValue] = useState(token)

  return (
    <div className="panel settings-panel">
      <h2>设置</h2>
      <div className="settings-row">
        <label>API Token</label>
        <input value={value} onChange={e => setValue(e.target.value)} />
        <button className="btn" onClick={() => setToken(value.trim())}>保存</button>
      </div>
      <p className="hint">修改 Token 后立即生效（存储于本机浏览器）。投屏画质/声音等设置在投屏面板内按设备单独保存。</p>
    </div>
  )
}
