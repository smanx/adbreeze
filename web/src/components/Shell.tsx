import { useEffect, useState } from 'react'
import { clearReconnectTimer, useStore } from '../store'
import { api, apiUrl, authHeaders, clearSession, isCrossOrigin, loadBackendConfig, normalizeBackendBase, probeBackend, backendBase, type AdbSettings, type AdbSettingsSaveResult } from '../api'
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
import { Icon } from './Icon'
import { toggleLang, useT } from '../i18n'

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
  const t = useT()

  // 后端配置变更（backendRevision 自增）时重建事件通道，切到新后端立即生效
  useEffect(() => {
    if (loggedOut) return
    watchEvents()
    return () => { clearReconnectTimer() }
  }, [loggedOut, backendRevision])

  // 退出：清除后端会话 cookie 并断开事件通道。
  // Basic 凭证由浏览器按源缓存、无法由脚本清除，故此处仅做降级退出提示。
  const logout = async () => {
    if (!confirm(t('shell.logoutConfirm'))) return
    clearReconnectTimer()
    await clearSession()
    setLoggedOut(true)
  }

  if (loggedOut) {
    return (
      <div className="auth-page">
        <div className="auth-card">
          <h1>{t('shell.signedOut')}</h1>
          <p className="auth-sub">{t('shell.signedOutHint')}</p>
          <button className="btn primary" onClick={() => location.reload()}>{t('shell.loginAgain')}</button>
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
              ? t('banner.unreachable', { host: backendBase() || location.host })
              : t('banner.auth')}
          </span>
          <span className="spacer" />
          <button className="btn tiny" onClick={() => setPanel('settings')}>{t('banner.configure')}</button>
          <button className="btn tiny ghost" onClick={retryBackend}>{t('common.retry')}</button>
        </div>
      )}
      <header className="topbar">
        <button className="icon-btn drawer-toggle" onClick={() => setDrawerOpen(true)}>☰</button>
        <span className="brand">ADBreeze</span>
        {version && <span className="version-badge">{version}</span>}
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
        <button className="btn ghost" onClick={logout}>{t('topbar.logout')}</button>
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
            className={'tab' + (panel === tab.id ? ' active' : '')}
            onClick={() => setPanel(tab.id)}
          >
            <Icon name={tab.id} />
            <span className="tab-txt">{t(tab.labelKey)}</span>
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

// 后端地址配置：默认与页面同源；跨源部署（前端托管在别的域名）时手动填写
// 后端地址与账号密码，保存后经 applyBackend 重建事件通道立即生效。
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
    if (norm && !draft.user.trim()) { setErr(t('settings.crossOriginNeedUser')); return }
    setBusy(true); setErr(''); setMsg('')
    const r = await probeBackend(norm)
    if (!r.ok) { setErr(t('settings.preSaveFailed')); setBusy(false); return }
    applyBackend({ base: norm, user: draft.user.trim(), pass: draft.pass })
    setMsg(t('settings.savedReconnecting'))
    setBusy(false)
  }

  const reset = () => {
    const c = { base: '', user: '', pass: '' }
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
          disabled={busy}
        />
        <input
          type="text"
          value={draft.user}
          placeholder={t('settings.userPlaceholder')}
          onChange={e => { setDraft({ ...draft, user: e.target.value }); setErr(''); setMsg('') }}
          disabled={busy}
        />
        <input
          type="password"
          value={draft.pass}
          onChange={e => { setDraft({ ...draft, pass: e.target.value }); setErr(''); setMsg('') }}
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
