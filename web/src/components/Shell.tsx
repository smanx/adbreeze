import { useEffect, useRef, useState } from 'react'
import { clearReconnectTimer, useStore } from '../store'
import { api, apiUrl, authHeaders, clearSession, isCrossOrigin, loadBackendConfig, login, normalizeBackendBase, probeBackend, backendBase, getTunnelStatus, getTunnelSettings, saveTunnelSettings, startTunnel, stopTunnel, getTunnelInstall, startTunnelInstall, type AdbSettings, type AdbSettingsSaveResult, type TunnelSettings, type TunnelSettingsSave, type TunnelStatus, type TunnelInstallStatus } from '../api'
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
import QrCode from './QrCode'
import { Icon } from './Icon'
import { toggleLang, useT } from '../i18n'

// 应用外壳：
//   PC（≥880px）：顶栏 + 左侧设备栏 + 主内容（标签页面板）
//   手机（<880px）：顶栏 + 设备抽屉 + 主内容 + 底部 TabBar
// 未登录 / 会话过期 / 主动退出时整屏切换到登录卡片（LoginCard）。
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
  const loginSucceeded = useStore(s => s.loginSucceeded)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [loggedOut, setLoggedOut] = useState(false)
  // 外网访问（Cloudflare Tunnel）状态：顶栏常驻徽章的数据源。
  // 10s 轻轮询（一个加锁快照，不碰 adb）；失败静默——徽章不值得打扰用户。
  const [tunnelOn, setTunnelOn] = useState(false)
  const t = useT()

  // 后端配置变更（backendRevision 自增）或重新登录时重建事件通道，
  // 切到新后端 / 新会话立即生效
  useEffect(() => {
    if (loggedOut) return
    watchEvents()
    return () => { clearReconnectTimer() }
  }, [loggedOut, backendRevision])

  // 隧道徽章轮询：仅在登录态进行
  useEffect(() => {
    if (loggedOut) return
    let alive = true
    const poll = async () => {
      try {
        const st = await getTunnelStatus()
        if (alive) setTunnelOn(st.running)
      } catch { /* 静默：后端不可达时徽章不显示 */ }
    }
    void poll()
    const timer = setInterval(poll, 10000)
    return () => { alive = false; clearInterval(timer) }
  }, [loggedOut])

  // 退出：清除后端会话 cookie 与本地 token 并断开事件通道。
  // 会话凭证只有这一处，退出即真失效——这是相对 Basic Auth 的核心改进。
  const logout = async () => {
    if (!confirm(t('shell.logoutConfirm'))) return
    clearReconnectTimer()
    await clearSession()
    setLoggedOut(true)
  }

  if (loggedOut || backendIssue === 'auth') {
    return (
      <LoginCard
        notice={loggedOut ? t('shell.signedOut') : ''}
        onSuccess={() => { setLoggedOut(false); loginSucceeded() }}
      />
    )
  }

  return (
    <div className="app-shell">
      {backendIssue === 'unreachable' && (
        <div className="backend-banner" role="alert">
          <span>{t('banner.unreachable', { host: backendBase() || location.host })}</span>
          <span className="spacer" />
          <button className="btn tiny" onClick={() => setPanel('settings')}>{t('banner.configure')}</button>
          <button className="btn tiny ghost" onClick={retryBackend}>{t('common.retry')}</button>
        </div>
      )}
      <header className="topbar">
        <button className="icon-btn drawer-toggle" onClick={() => setDrawerOpen(true)}>☰</button>
        <span className="brand">ADBreeze</span>
        {version && <span className="version-badge">{version}</span>}
        {tunnelOn && (
          <button
            className="btn tiny ghost tunnel-badge has-tip"
            data-tip={t('tunnel.badgeTip')}
            aria-label={t('tunnel.badge')}
            onClick={() => setPanel('settings')}
          >
            <Icon name="globe" /><span className="tunnel-badge-txt">{t('tunnel.badge')}</span>
          </button>
        )}
        <span className={'conn-dot' + (serverConnected ? ' ok' : '')} title={serverConnected ? t('topbar.serviceOnline') : t('topbar.reconnecting')} />
        <span className="current-device">{selected || t('topbar.noDevice')}</span>
        <span className="spacer" />
        <nav className="topbar-tabs">
          {TABS.map(tab => (
            <button
              key={tab.id}
              className={'tab has-tip' + (panel === tab.id ? ' active' : '')}
              data-tip={t(tab.labelKey)}
              aria-label={t(tab.labelKey)}
              onClick={() => setPanel(tab.id)}
            ><Icon name={tab.id} /></button>
          ))}
        </nav>
        <button className="btn tiny ghost lang-btn" onClick={() => toggleLang()} title={t('topbar.langTip')}>{t('topbar.langBtn')}</button>
        <a
          className="icon-btn gh-link has-tip"
          data-tip={t('topbar.github')}
          href="https://github.com/smanx/adbreeze"
          target="_blank"
          rel="noopener noreferrer"
          aria-label={t('topbar.github')}
        ><Icon name="github" /></a>
        <button
          className="btn ghost logout-btn has-tip"
          data-tip={t('topbar.logout')}
          aria-label={t('topbar.logout')}
          onClick={logout}
        >
          <Icon name="logout" />
          <span className="logout-txt">{t('topbar.logout')}</span>
        </button>
      </header>

      <div className="app-body">
        <aside className="sidebar">
          <h3 className="side-title">{t('topbar.devices')}</h3>
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
            : <Placeholder title={t(TABS.find(tab => tab.id === panel)!.labelKey)}
                phase={TABS.find(tab => tab.id === panel)!.phase || t('placeholder.laterPhase')} selected={selected} />}
        </main>
      </div>

      {drawerOpen && (
        <>
          <div className="drawer-mask" onClick={() => setDrawerOpen(false)} />
          <aside className="drawer">
            <h3 className="side-title">{t('topbar.devices')}</h3>
            <DeviceList />
          </aside>
        </>
      )}

      <nav className="tabbar">
        {TABS.map(tab => (
          <button
            key={tab.id}
            className={'tab has-tip' + (panel === tab.id ? ' active' : '')}
            data-tip={t(tab.labelKey)}
            aria-label={t(tab.labelKey)}
            onClick={() => setPanel(tab.id)}
          >
            <Icon name={tab.id} />
          </button>
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
  const t = useT()

  const load = async () => {
    try {
      const r = await api<AdbSettings>('GET', '/api/settings/adb')
      setInfo(r)
      setDraft(r.path)
      setErr('')
    } catch (e) {
      setErr(t('settings.adbReadFailed') + (e as Error).message)
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
    } finally { setBusy(false) }
  }

  const usingDefault = !!info && info.path === ''

  return (
    <div className="panel settings-panel">
      <h2>{t('settings.title')}</h2>

      <BackendSettings />

      <TlsSettings />

      <TunnelSettings />

      <div className="settings-block">
        <h3>{t('settings.adbPath')}</h3>
        <p className="hint">
          {t('settings.adbHint')}
        </p>
        <div className="adb-path-row">
          <input
            type="text"
            value={draft}
            placeholder={info?.available ? t('settings.adbDefaultPrefix') + info.default : t('settings.adbNotFound')}
            onChange={e => { setDraft(e.target.value); setErr(''); setMsg('') }}
            onKeyDown={e => e.key === 'Enter' && !busy && save(draft.trim())}
            disabled={busy}
          />
          <button className="btn primary" disabled={busy || usingDefault && draft.trim() === ''}
            onClick={() => save(draft.trim())}>
            {busy ? t('settings.switching') : t('settings.saveSwitch')}
          </button>
          <button className="btn" disabled={busy || usingDefault}
            onClick={() => save('')} title={t('settings.usePathTitle')}>
            {t('settings.resetDefault')}
          </button>
        </div>
        {err && <p className="hint error-text">{err}</p>}
        {msg && <p className="hint ok-text">{msg}</p>}
        {info && (
          <p className="hint">
            {t('settings.active')}<code>{info.path || t('settings.defaultWith', { x: info.available ? info.default : t('settings.notInPath') })}</code>
            {'　'}{t('settings.saved')}<code>{info.configFile || t('settings.noPersistence')}</code>
          </p>
        )}
      </div>

      <p className="hint">
        {t('settings.authHint')}
      </p>
    </div>
  )
}

// 登录卡片：管理员密码登录（POST /api/session）。
// 三种场景共用：未加载到有效会话（首次进入 / 12h 过期）、会话被拒（跨源 token
// 失效等）、主动退出后重新登录（notice 显示「已退出」）。
// 连续失败 5 次后端锁定 30 秒（429 + Retry-After），界面同步倒计时。
function LoginCard({ notice, onSuccess }: { notice?: string; onSuccess: () => void }) {
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [locked, setLocked] = useState(0)
  const t = useT()

  // 锁定倒计时
  useEffect(() => {
    if (locked <= 0) return
    const timer = setTimeout(() => setLocked(s => s - 1), 1000)
    return () => clearTimeout(timer)
  }, [locked])

  const submit = async () => {
    if (busy || locked > 0 || !password) return
    setBusy(true); setErr('')
    const r = await login(password)
    setBusy(false)
    if (r.ok) { onSuccess(); return }
    setPassword('')
    if (r.lockedFor) setLocked(r.lockedFor)
    else setErr(r.error ? t('login.rejected') : t('login.networkError'))
  }

  return (
    <div className="auth-page">
      <div className="auth-card">
        <h1>{t('login.title')}</h1>
        {notice && <p className="auth-sub">{notice}</p>}
        <input
          type="password"
          className="auth-input"
          autoFocus
          placeholder={t('login.password')}
          value={password}
          onChange={e => { setPassword(e.target.value); setErr('') }}
          onKeyDown={e => e.key === 'Enter' && void submit()}
          disabled={busy || locked > 0}
        />
        {locked > 0 ? (
          <p className="auth-error">{t('login.locked', { s: locked })}</p>
        ) : err ? (
          <p className="auth-error">{err}</p>
        ) : null}
        <button className="btn primary" disabled={busy || locked > 0 || !password} onClick={() => void submit()}>
          {busy ? t('login.submitting') : t('login.submit')}
        </button>
      </div>
    </div>
  )
}

// 后端地址配置：默认与页面同源；跨源部署（前端托管在别的域名）时手动填写
// 后端地址，保存后经 applyBackend 重建事件通道立即生效。
// 登录在页面内完成（管理员密码），这里不再收集账号密码——切换后端后
// 旧 token 对新后端无效，会自动回到登录界面。
function BackendSettings() {
  const applyBackend = useStore(s => s.applyBackend)
  const [draft, setDraft] = useState(() => loadBackendConfig())
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const t = useT()

  // 仅探活（/api/health 无需鉴权）：验证地址可达
  const test = async () => {
    setBusy(true); setErr(''); setMsg('')
    const r = await probeBackend(draft.base)
    if (r.ok) setMsg(t('settings.connected') + (r.version ? t('settings.backendOf', { v: r.version }) : ''))
    else setErr(t('settings.connectFailed') + (r.error || t('settings.unknownError')))
    setBusy(false)
  }

  // 保存并重连：先探活再落盘，避免把明显不可达的地址存进去
  const save = async () => {
    const norm = normalizeBackendBase(draft.base)
    if (norm === null) { setErr(t('settings.badAddress')); return }
    setBusy(true); setErr(''); setMsg('')
    const r = await probeBackend(norm)
    if (!r.ok) { setErr(t('settings.preSaveFailed')); setBusy(false); return }
    applyBackend({ base: norm })
    setMsg(t('settings.savedReconnecting'))
    setBusy(false)
  }

  const reset = () => {
    const c = { base: '' }
    setDraft(c)
    applyBackend(c)
    setErr(''); setMsg(t('settings.backSameOrigin'))
  }

  return (
    <div className="settings-block">
      <h3>{t('settings.backend')}</h3>
      <p className="hint">
        {t('settings.backendHint', { host: location.host })}
      </p>
      <div className="adb-path-row">
        <input
          type="text"
          value={draft.base}
          placeholder={t('settings.sameOriginEmpty', { host: location.host })}
          onChange={e => { setDraft({ ...draft, base: e.target.value }); setErr(''); setMsg('') }}
          onKeyDown={e => e.key === 'Enter' && void save()}
          disabled={busy}
        />
      </div>
      <div className="adb-path-row">
        <button className="btn" disabled={busy} onClick={() => void test()}>
          {busy ? t('settings.testing') : t('settings.testConnection')}
        </button>
        <button className="btn primary" disabled={busy} onClick={() => void save()}>
          {busy ? t('settings.testing') : t('settings.saveReconnect')}
        </button>
        <button className="btn ghost" disabled={busy} onClick={reset} title={t('settings.backSameOriginTip')}>{t('settings.backSameOrigin')}</button>
      </div>
      {err && <p className="hint error-text">{err}</p>}
      {msg && <p className="hint ok-text">{msg}</p>}
      <p className="hint">{t('settings.active')}<code>{backendBase() || t('settings.activeSameOrigin', { host: location.host })}</code></p>
    </div>
  )
}

interface TlsStatus {
  https: boolean
  custom: boolean
  caFile: string
  caFingerprint: string
  dns: string[]
  ips: string[]
  urls: string[]
}

// HTTPS 证书安装引导：自签 CA 导入系统信任库后浏览器不再告警「页面不受信任」。
// PC（Win/macOS/Linux）、安卓（用户 CA 存储）、iOS（描述文件 + 证书信任开关）均适用；
// CA 长期不变，安装一次后换网/换 IP/重启都不用再装。
function TlsSettings() {
  const [info, setInfo] = useState<TlsStatus | null>(null)
  const [err, setErr] = useState('')
  const t = useT()

  const load = async () => {
    try {
      setInfo(await api<TlsStatus>('GET', '/api/settings/tls'))
      setErr('')
    } catch (e) {
      setErr(t('settings.tlsReadFailed') + (e as Error).message)
    }
  }

  useEffect(() => { void load() }, [])

  // 下载 CA / 描述文件。同源走导航（自动带凭证；iOS 只在导航时才弹「安装描述文件」）；
  // 跨源导航带不上 Authorization 头，改 fetch 取 blob。
  const download = async (path: string, filename: string | null) => {
    if (!isCrossOrigin()) {
      const a = document.createElement('a')
      a.href = apiUrl(path)
      if (filename) a.download = filename
      a.click()
      return
    }
    try {
      const res = await fetch(apiUrl(path), { headers: authHeaders() })
      if (!res.ok) throw new Error('HTTP ' + res.status)
      const blob = await res.blob()
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = filename || 'adbreeze-ca'
      a.click()
      setTimeout(() => URL.revokeObjectURL(a.href), 1000)
    } catch (e) {
      setErr(t('settings.downloadFailed') + (e as Error).message)
    }
  }

  if (!info) {
    return (
      <div className="settings-block">
        <h3>{t('settings.tls')}</h3>
        <p className="hint">{err || t('common.loading')}</p>
      </div>
    )
  }

  if (!info.https) {
    return (
      <div className="settings-block">
        <h3>{t('settings.tls')}</h3>
        <p className="hint">
          {t('settings.tlsOff')}
        </p>
      </div>
    )
  }

  if (info.custom) {
    return (
      <div className="settings-block">
        <h3>{t('settings.tls')}</h3>
        <p className="hint">
          {t('settings.tlsCustom')}
        </p>
      </div>
    )
  }

  const host = location.hostname
  const covered = info.dns.some(d => d.toLowerCase() === host.toLowerCase()) || info.ips.includes(host)

  return (
    <div className="settings-block">
      <h3>{t('settings.tls')}</h3>
      <p className="hint">
        {t('settings.tlsIntro')}
      </p>
      <div className="adb-path-row">
        <button className="btn primary" onClick={() => void download('/api/settings/tls/ca.crt', 'adbreeze-ca.crt')}>
          {t('settings.downloadCa')}
        </button>
        <button className="btn" onClick={() => void download('/api/settings/tls/ca.mobileconfig', null)} title={t('settings.iosOnly')}>
          {t('settings.downloadMobileconfig')}
        </button>
      </div>
      <p className="hint">
        {t('settings.caFingerprint')}<code>{info.caFingerprint || t('settings.fingerprintUnknown')}</code>
      </p>
      <p className="hint">
        {t('settings.certHosts')}<code>{info.dns.join('、') || t('settings.certNone')}</code>
        {t('settings.certIps')}<code>{info.ips.join('、') || t('settings.certNone')}</code>
        {!covered && (
          <><br /><span className="error-text">
            {t('settings.hostNotCovered', { host })}
          </span></>
        )}
      </p>
      <details className="cert-help">
        <summary>{t('settings.installHelp')}</summary>
        <ul>
          <li>{t('settings.installWin')}</li>
          <li>{t('settings.installMac')}</li>
          <li>{t('settings.installLinux')}</li>
          <li>{t('settings.installAndroid')}</li>
          <li>{t('settings.installIos')}</li>
        </ul>
      </details>
      {err && <p className="hint error-text">{err}</p>}
    </div>
  )
}

// 运行时长格式化：startedAt（RFC3339）→ "1h 05m" / "3m 20s" / "45s"
function formatUptime(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(ms) || ms < 0) return ''
  const s = Math.floor(ms / 1000)
  if (s < 60) return s + 's'
  const m = Math.floor(s / 60)
  if (m < 60) return m + 'm ' + (s % 60) + 's'
  return Math.floor(m / 60) + 'h ' + (m % 60) + 'm'
}

// 字节数 → "12.3 MB"
function fmtMB(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 MB'
  return (n / 1048576).toFixed(1) + ' MB'
}

type TunnelStateKey = 'tunnel.state.idle' | 'tunnel.state.starting' | 'tunnel.state.running' | 'tunnel.state.stopped' | 'tunnel.state.failed'

const TUNNEL_STATE_KEYS: Record<string, TunnelStateKey> = {
  idle: 'tunnel.state.idle',
  starting: 'tunnel.state.starting',
  running: 'tunnel.state.running',
  stopped: 'tunnel.state.stopped',
  failed: 'tunnel.state.failed',
}

// 外网访问（Cloudflare Tunnel）设置卡片：状态区 + 配置区 + 控制区。
// cloudflared 不内置（安装包 +0）：未安装时给分平台安装指引，也可手动填路径。
// 开启的唯一门槛是本机有 cloudflared；默认密码不拦截，仅展示提醒（后端记 WARN）。
function TunnelSettings() {
  const [info, setInfo] = useState<TunnelSettings | null>(null)
  const [status, setStatus] = useState<TunnelStatus | null>(null)
  const [inst, setInst] = useState<TunnelInstallStatus | null>(null)
  const instStateRef = useRef('') // 记住上一次见到的安装状态：done 只提示一次
  const [draft, setDraft] = useState({ binary: '', service: '', protocol: '', token: '', autoRestart: true })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const t = useT()

  const load = async () => {
    try {
      const r = await getTunnelSettings()
      setInfo(r)
      // token 不回显全文：输入框留空，未重打则不传（保持原值）
      setDraft({ binary: r.binary, service: r.service, protocol: r.protocol, token: '', autoRestart: r.autoRestart })
      setErr('')
    } catch (e) {
      setErr(t('tunnel.readFailed') + (e as Error).message)
    }
  }

  const loadStatus = async () => {
    try { setStatus(await getTunnelStatus()) } catch { /* 轮询失败静默 */ }
  }

  // 一键安装状态：done 时刷新设置（新二进制立刻被探测到）并提示一次
  const loadInstall = async () => {
    try {
      const st = await getTunnelInstall()
      setInst(st)
      if (st.state === 'done' && instStateRef.current !== 'done') {
        setMsg(t('tunnel.installDone', { v: st.version || 'cloudflared', p: st.path }))
        await load()
      }
      instStateRef.current = st.state
    } catch { /* 轮询失败静默 */ }
  }

  useEffect(() => { void load(); void loadStatus(); void loadInstall() }, [])

  // 启动中 / 已连接期间 3s 轮询状态；停止后停轮询
  useEffect(() => {
    if (status?.state !== 'running' && status?.state !== 'starting') return
    const timer = setInterval(() => void loadStatus(), 3000)
    return () => clearInterval(timer)
  }, [status?.state])

  // 安装期间 1s 轮询安装进度
  const installing = inst?.state === 'downloading' || inst?.state === 'verifying'
  useEffect(() => {
    if (!installing) return
    const timer = setInterval(() => void loadInstall(), 1000)
    return () => clearInterval(timer)
  }, [installing])

  // 保存：只传表单字段；token 未重打则不传（保持原值），extra 用于「清除 token」
  const save = async (extra?: TunnelSettingsSave) => {
    setBusy(true); setErr(''); setMsg('')
    try {
      const body: TunnelSettingsSave = {
        binary: draft.binary.trim(),
        service: draft.service.trim(),
        protocol: draft.protocol.trim(),
        autoRestart: draft.autoRestart,
        ...extra,
      }
      if (draft.token !== '') body.token = draft.token.trim()
      const r = await saveTunnelSettings(body)
      setInfo(r)
      setDraft(d => ({ ...d, token: '' }))
      setMsg(r.message || t('common.done'))
      await loadStatus()
    } catch (e) {
      setErr(t('tunnel.saveFailed') + (e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const start = async () => {
    if (!confirm(t('tunnel.confirmStart'))) return
    setBusy(true); setErr(''); setMsg('')
    try {
      setStatus(await startTunnel())
    } catch (e) {
      setErr(t('tunnel.startFailed') + (e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const stop = async () => {
    setBusy(true); setErr(''); setMsg('')
    try {
      setStatus(await stopTunnel())
    } catch (e) {
      setErr(t('tunnel.stopFailed') + (e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // 一键安装 cloudflared：后台下载，前端 1s 轮询进度（见 loadInstall）
  const install = async () => {
    setErr(''); setMsg('')
    try {
      setInst(await startTunnelInstall())
    } catch (e) {
      setErr(t('tunnel.installFailed') + (e as Error).message)
    }
  }

  const copyUrl = async () => {
    if (!status?.url) return
    try {
      await navigator.clipboard.writeText(status.url)
      setMsg(t('tunnel.copied'))
    } catch {
      setErr(t('tunnel.copyFailed'))
    }
  }

  if (!info) {
    return (
      <div className="settings-block">
        <h3>{t('tunnel.title')}</h3>
        <p className="hint">{err || t('common.loading')}</p>
      </div>
    )
  }

  const stateKey: TunnelStateKey = TUNNEL_STATE_KEYS[status?.state ?? 'idle'] ?? 'tunnel.state.idle'
  const dotCls = 'conn-dot'
    + (status?.running ? ' ok' : status?.state === 'failed' ? ' err' : '')
  const uptime = status?.running && status.startedAt ? formatUptime(status.startedAt) : ''
  const canStart = info.available

  return (
    <div className="settings-block">
      <h3>{t('tunnel.title')}</h3>
      <p className="hint">{t('tunnel.intro')}</p>

      {/* 状态区 */}
      <div className="adb-path-row">
        <span className={dotCls} />
        <b>{t(stateKey)}</b>
        {status?.mode && <span className="hint">{status.mode === 'named' ? t('tunnel.mode.named') : t('tunnel.mode.quick')}</span>}
        {!!status?.restarts && <span className="hint">{t('tunnel.restarts', { n: status.restarts })}</span>}
        {uptime && <span className="hint">{t('tunnel.uptime', { s: uptime })}</span>}
        <span className="spacer" />
        {status?.running ? (
          <button className="btn danger tiny" disabled={busy} onClick={() => void stop()}>
            {busy ? t('tunnel.busyStop') : t('tunnel.stop')}
          </button>
        ) : (
          <button className="btn primary tiny" disabled={busy || !canStart} onClick={() => void start()} title={canStart ? '' : t('tunnel.noBinary')}>
            {busy ? t('tunnel.busyStart') : t('tunnel.start')}
          </button>
        )}
      </div>

      {/* 外网地址（quick 模式本地可感知；named 模式域名在 CF 侧） */}
      {status?.running && status.url && (
        <div className="adb-path-row">
          <span className="hint">{t('tunnel.urlLabel')}</span>
          <code className="tunnel-url">{status.url}</code>
          <button className="btn tiny ghost" onClick={() => void copyUrl()}>{t('tunnel.copy')}</button>
        </div>
      )}
      {status?.running && status.url && (
        <div className="tunnel-qr-row">
          <QrCode text={status.url} size={132} />
          <p className="hint">{t('tunnel.qrHint')}</p>
        </div>
      )}
      {status?.running && status.mode === 'named' && (
        <p className="hint">{t('tunnel.namedDomainHint')}</p>
      )}
      {status?.error && <p className="hint error-text">{status.error}</p>}

      {/* 门槛提示：只有 cloudflared 缺失才禁住开启按钮；默认密码仅提醒不拦截 */}
      {!info.available && <p className="hint error-text">{t('tunnel.noBinary')}</p>}
      {info.weakPassword && <p className="hint">{t('tunnel.defaultPassword')}</p>}
      {info.available && (
        <p className="hint">
          {info.version ? t('tunnel.detected', { p: info.resolved, v: info.version }) : t('tunnel.detectedNoVersion', { p: info.resolved })}
        </p>
      )}

      {/* 配置区 */}
      <div className="adb-path-row">
        <input
          type="text"
          value={draft.binary}
          placeholder={t('tunnel.binaryPlaceholder')}
          onChange={e => { setDraft({ ...draft, binary: e.target.value }); setErr(''); setMsg('') }}
          disabled={busy}
        />
        <button
          className="btn"
          disabled={busy || installing}
          onClick={() => void install()}
          title={t('tunnel.installTip', { p: info.platform || 'this' })}
        >
          {installing ? t('tunnel.installing') : t('tunnel.install')}
        </button>
      </div>
      {installing && (
        <>
          <div className="tunnel-progress">
            <div className="tunnel-progress-fill" style={{ width: (inst?.progress || 0) + '%' }} />
          </div>
          <p className="hint">
            {inst?.state === 'verifying'
              ? t('tunnel.installVerifying')
              : inst && inst.total > 0
                ? t('tunnel.installProgressKnown', { n: inst.progress, d: fmtMB(inst.downloaded), t: fmtMB(inst.total) })
                : t('tunnel.installProgressUnknown', { d: fmtMB(inst?.downloaded || 0) })}
          </p>
        </>
      )}
      {!installing && inst?.state === 'failed' && <p className="hint error-text">{t('tunnel.installFailed') + inst.error}</p>}
      {!installing && inst?.state === 'done' && inst.path && (
        <p className="hint">
          {t('tunnel.installedTo')}<code>{inst.path}</code>
          {inst.sha256 && <>　SHA256 <code>{inst.sha256.slice(0, 16)}…</code></>}
        </p>
      )}
      <p className="hint">{t('tunnel.binaryHint')}</p>

      <div className="adb-path-row">
        <input
          type="password"
          value={draft.token}
          placeholder={info.tokenSet ? t('tunnel.tokenConfigured', { mask: info.tokenMask }) : t('tunnel.token')}
          onChange={e => { setDraft({ ...draft, token: e.target.value }); setErr(''); setMsg('') }}
          disabled={busy}
        />
        {info.tokenSet && (
          <button className="btn tiny ghost" disabled={busy} onClick={() => void save({ token: '' })}>
            {t('tunnel.tokenClear')}
          </button>
        )}
      </div>
      <p className="hint">{t('tunnel.tokenHint')}</p>

      <details className="cert-help">
        <summary>{t('tunnel.advanced')}</summary>
        <div className="adb-path-row">
          <input
            type="text"
            value={draft.service}
            placeholder="http://127.0.0.1:9080"
            onChange={e => { setDraft({ ...draft, service: e.target.value }); setErr(''); setMsg('') }}
            disabled={busy}
          />
        </div>
        <p className="hint">{t('tunnel.serviceHint')}</p>
        <div className="adb-path-row">
          <select
            value={draft.protocol}
            onChange={e => { setDraft({ ...draft, protocol: e.target.value }); setErr(''); setMsg('') }}
            disabled={busy}
          >
            <option value="">quic（{t('common.default')}）</option>
            <option value="http2">http2</option>
          </select>
        </div>
        <p className="hint">{t('tunnel.protocolHint')}</p>
        <label className="tunnel-check">
          <input
            type="checkbox"
            checked={draft.autoRestart}
            onChange={e => { setDraft({ ...draft, autoRestart: e.target.checked }); setErr(''); setMsg('') }}
            disabled={busy}
          />
          {t('tunnel.autoRestart')}
        </label>
      </details>

      {/* 自启语义：记住上次状态（无开关，由开启/关闭动作维护） */}
      <p className="hint">
        {status?.running || status?.state === 'starting'
          ? t('tunnel.resumeWillRestore')
          : t('tunnel.resumeWillNotRestore')}
      </p>

      <div className="adb-path-row">
        <button className="btn primary" disabled={busy} onClick={() => void save()}>
          {busy ? t('tunnel.saving') : t('tunnel.save')}
        </button>
      </div>
      {err && <p className="hint error-text">{err}</p>}
      {msg && <p className="hint ok-text">{msg}</p>}

      {/* 日志区 */}
      <details className="cert-help">
        <summary>{t('tunnel.logs')}</summary>
        <pre className="tunnel-logs">{status?.logs.length ? status.logs.join('\n') : t('tunnel.noLogs')}</pre>
      </details>
    </div>
  )
}
