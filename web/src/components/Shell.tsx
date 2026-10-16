import { useEffect, useState } from 'react'
import { clearReconnectTimer, useStore } from '../store'
import { api, clearSession, loadBackendConfig, normalizeBackendBase, probeBackend, backendBase, type AdbSettings, type AdbSettingsSaveResult } from '../api'
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
  const backendIssue = useStore(s => s.backendIssue)
  const backendRevision = useStore(s => s.backendRevision)
  const retryBackend = useStore(s => s.retryBackend)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [loggedOut, setLoggedOut] = useState(false)

  // 后端配置变更（backendRevision 自增）时重建事件通道，切到新后端立即生效
  useEffect(() => {
    if (loggedOut) return
    watchEvents()
    return () => { clearReconnectTimer() }
  }, [loggedOut, backendRevision])

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
      {backendIssue !== 'none' && (
        <div className="backend-banner" role="alert">
          <span>
            {backendIssue === 'unreachable'
              ? `无法连接后端（${backendBase() || location.host}）：请检查网络是否连通，或在「设置」中手动配置后端地址`
              : '后端拒绝了当前账号密码：跨源访问时浏览器不会弹出登录框，请在「设置」中核对后端地址、账号与密码'}
          </span>
          <span className="spacer" />
          <button className="btn tiny" onClick={() => setPanel('settings')}>去配置</button>
          <button className="btn tiny ghost" onClick={retryBackend}>重试</button>
        </div>
      )}
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

      <BackendSettings />

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

// 后端地址配置：默认与页面同源；跨源部署（前端托管在别的域名）时手动填写
// 后端地址与账号密码，保存后经 applyBackend 重建事件通道立即生效。
function BackendSettings() {
  const applyBackend = useStore(s => s.applyBackend)
  const [draft, setDraft] = useState(() => loadBackendConfig())
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')

  // 仅探活（/api/health 无需鉴权）：验证地址可达
  const test = async () => {
    setBusy(true); setErr(''); setMsg('')
    const r = await probeBackend(draft.base)
    if (r.ok) setMsg('连接成功' + (r.version ? `（后端 v${r.version}）` : ''))
    else setErr('连接失败：' + (r.error || '未知错误'))
    setBusy(false)
  }

  // 保存并重连：先探活再落盘，避免把明显不可达的地址存进去
  const save = async () => {
    const norm = normalizeBackendBase(draft.base)
    if (norm === null) { setErr('地址格式不正确：需以 http:// 或 https:// 开头'); return }
    if (norm && !draft.user.trim()) { setErr('跨源访问需填写账号密码（浏览器不会弹出登录框）'); return }
    setBusy(true); setErr(''); setMsg('')
    const r = await probeBackend(norm)
    if (!r.ok) { setErr('保存前连接测试失败：' + (r.error || '未知错误')); setBusy(false); return }
    applyBackend({ base: norm, user: draft.user.trim(), pass: draft.pass })
    setMsg('已保存，正在重连…')
    setBusy(false)
  }

  const reset = () => {
    const c = { base: '', user: '', pass: '' }
    setDraft(c)
    applyBackend(c)
    setErr(''); setMsg('已恢复为同源访问')
  }

  return (
    <div className="settings-block">
      <h3>后端地址</h3>
      <p className="hint">
        默认与页面同源（当前 <code>{location.host}</code>），由浏览器自动携带登录凭证。
        前端托管在别的地址时（静态 Pages、反代等），填写后端地址（如 <code>http://192.168.1.5:9080</code>）
        并保存，之后所有请求与投屏流都打到该后端。
        注意：HTTPS 页面无法访问 HTTP 后端（浏览器混合内容拦截），此时应填后端的 HTTPS 地址
        （本服务默认同时监听 :9443，需先在浏览器信任其证书）。
        跨源时账号密码仅保存在本浏览器 localStorage，用于拼装 Authorization 头。
      </p>
      <div className="adb-path-row">
        <input
          type="text"
          value={draft.base}
          placeholder={'留空 = 与页面同源（' + location.host + '）'}
          onChange={e => { setDraft({ ...draft, base: e.target.value }); setErr(''); setMsg('') }}
          disabled={busy}
        />
        <input
          type="text"
          value={draft.user}
          placeholder="账号（跨源必填）"
          onChange={e => { setDraft({ ...draft, user: e.target.value }); setErr(''); setMsg('') }}
          disabled={busy}
        />
        <input
          type="password"
          value={draft.pass}
          placeholder="密码（跨源必填）"
          onChange={e => { setDraft({ ...draft, pass: e.target.value }); setErr(''); setMsg('') }}
          onKeyDown={e => e.key === 'Enter' && !busy && void save()}
          disabled={busy}
        />
      </div>
      <div className="adb-path-row">
        <button className="btn" disabled={busy} onClick={() => void test()}>
          {busy ? '测试中…' : '测试连接'}
        </button>
        <button className="btn primary" disabled={busy} onClick={() => void save()}>
          {busy ? '保存中…' : '保存并重连'}
        </button>
        <button className="btn ghost" disabled={busy} onClick={reset} title="改回与页面同源">
          恢复同源
        </button>
      </div>
      {err && <p className="hint error-text">{err}</p>}
      {msg && <p className="hint ok-text">{msg}</p>}
      <p className="hint">当前生效：<code>{backendBase() || '同源（' + location.host + '）'}</code></p>
    </div>
  )
}
