import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, apiUrl, authHeaders } from '../api'
import { useStore } from '../store'
import { Th, useColWidths } from '../components/TableTh'
import { useT, type MsgKey } from '../i18n'

// 应用面板（对齐 AYA Application）：
// 列表列：应用名/包名/版本/最小SDK/存储占用/状态 + 系统应用开关 + 搜索。
// 右键菜单：应用信息/导出安装包/打开/停止/冻结/解冻/清除数据/卸载。
// 详情弹窗：应用名/包名/版本/系统/SDK/时间/APK 大小/占用三项/APK 路径。
interface AppEntry {
  package: string
  label?: string
  versionName?: string
  minSdk?: number
  targetSdk?: number
  appSize?: number
  dataSize?: number
  cacheSize?: number
  system: boolean
  disabled: boolean
  apkPath?: string
}
interface AppInfo {
  package: string
  label?: string
  versionName?: string
  versionCode?: number
  minSdk?: number
  targetSdk?: number
  apkPath?: string
  firstInstall?: string
  lastUpdate?: string
  system: boolean
  enabled: boolean
}

type SortKey = 'label' | 'package' | 'versionName' | 'minSdk' | 'targetSdk' | 'storage' | 'status'
const APP_KEYS: SortKey[] = ['label', 'package', 'versionName', 'minSdk', 'targetSdk', 'storage', 'status']
const APP_COLS: MsgKey[] = ['apps.colName', 'apps.colPackage', 'apps.colVersion', 'apps.colMinSdk', 'apps.colTargetSdk', 'apps.colStorage', 'common.status']
// 右对齐列（SDK/存储）
const APP_NUM_COLS = [3, 4, 5]

function fmtSize(n?: number): string {
  if (!n) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0, v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`
}
function storageOf(a: AppEntry): number {
  return (a.appSize || 0) + (a.dataSize || 0) + (a.cacheSize || 0)
}

export default function AppsPanel() {
  const selected = useStore(s => s.selected)
  const [apps, setApps] = useState<AppEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const [showSys, setShowSys] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [hint, setHint] = useState('')
  const [busyPkg, setBusyPkg] = useState('')
  const [info, setInfo] = useState<AppInfo | null>(null)
  const [menu, setMenu] = useState<{ x: number; y: number; app: AppEntry } | null>(null)
  const [sortKey, setSortKey] = useState<SortKey>('label')
  const [sortAsc, setSortAsc] = useState(true)
  const [widths, setWidth] = useColWidths('adbreeze-apps-colwidths', APP_COLS.length)
  const menuRef = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const t = useT()

  const load = useCallback(async (refresh = false) => {
    if (!selected) return
    setLoading(true); setErr('')
    try {
      setApps(await api<AppEntry[]>('GET',
        `/api/devices/${encodeURIComponent(selected)}/apps` + (refresh ? '?refresh=1' : '')))
    } catch (e) { setErr((e as Error).message) } finally { setLoading(false) }
  }, [selected])

  useEffect(() => { setApps([]); setInfo(null); load() }, [load])

  // 全局点击关闭右键菜单
  useEffect(() => {
    if (!menu) return
    const close = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenu(null)
    }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [menu])

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    let list = apps.filter(a => (showSys || !a.system) &&
      (!kw || a.package.toLowerCase().includes(kw) || (a.label || '').toLowerCase().includes(kw)))
    const dir = sortAsc ? 1 : -1
    list = [...list].sort((a, b) => {
      switch (sortKey) {
        case 'label': return dir * (a.label || a.package).localeCompare(b.label || b.package)
        case 'package': return dir * a.package.localeCompare(b.package)
        case 'versionName': return dir * (a.versionName || '').localeCompare(b.versionName || '')
        case 'minSdk': return dir * ((a.minSdk || 0) - (b.minSdk || 0))
        case 'targetSdk': return dir * ((a.targetSdk || 0) - (b.targetSdk || 0))
        case 'storage': return dir * (storageOf(a) - storageOf(b))
        case 'status': return dir * ((a.disabled ? 1 : 0) - (b.disabled ? 1 : 0))
      }
    })
    return list
  }, [apps, showSys, keyword, sortKey, sortAsc])

  const clickHeader = (k: SortKey) => {
    if (sortKey === k) setSortAsc(a => !a)
    else { setSortKey(k); setSortAsc(true) }
  }

  const th = (key: SortKey, text: string, i: number) => (
    <Th colKey={key} text={text}
      sort={{ key: sortKey, asc: sortAsc }} onSort={clickHeader}
      width={widths[i] || undefined} onResize={w => setWidth(i, w)}
      align={APP_NUM_COLS.includes(i) ? 'right' : 'left'} />
  )

  const doAction = async (pkg: string, action: string, confirmMsg?: string) => {
    if (confirmMsg && !confirm(confirmMsg)) return
    setBusyPkg(pkg); setHint(''); setMenu(null)
    try {
      const r = await api<{ message: string }>('POST', `/api/devices/${encodeURIComponent(selected!)}/apps/action`, { package: pkg, action })
      setHint(`${pkg}: ${r.message || t('common.done')}`)
      if (action !== 'open') load() // 状态类操作后刷新列表
    } catch (e) { setHint(t('apps.actionFailed', { pkg, msg: (e as Error).message })) } finally { setBusyPkg('') }
  }

  const openInfo = async (pkg: string) => {
    setMenu(null)
    setBusyPkg(pkg); setHint('')
    try {
      setInfo(await api<AppInfo>('GET', `/api/devices/${encodeURIComponent(selected!)}/apps/info?package=${encodeURIComponent(pkg)}`))
    } catch (e) { setHint((e as Error).message) } finally { setBusyPkg('') }
  }

  const exportApk = async (app: AppEntry) => {
    setMenu(null)
    setBusyPkg(app.package); setHint(t('apps.exporting'))
    try {
      const blob = await fetch(apiUrl(`/api/devices/${encodeURIComponent(selected!)}/apps/apk?package=${encodeURIComponent(app.package)}`), { headers: authHeaders() })
        .then(r => { if (!r.ok) throw new Error('HTTP ' + r.status); return r.blob() })
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      // AYA 同款命名：包名-版本.apk
      a.download = app.versionName ? `${app.package}-${app.versionName}.apk` : `${app.package}.apk`
      a.click()
      URL.revokeObjectURL(a.href)
      setHint(t('apps.exportSaved', { pkg: app.package }))
    } catch (e) { setHint(t('apps.exportFailed', { pkg: app.package, msg: (e as Error).message })) } finally { setBusyPkg('') }
  }

  const confirmPrefix = (a: AppEntry) => a.system ? t('apps.sysPrefix') : ''
  const showName = (a: AppEntry) => a.label || a.package

  const menuItems = (a: AppEntry) => ([
    { label: t('apps.menuInfo'), act: () => openInfo(a.package) },
    { label: t('apps.menuExport'), act: () => exportApk(a) },
    { sep: true },
    { label: t('apps.menuOpen'), act: () => doAction(a.package, 'open') },
    { label: t('apps.menuStop'), act: () => doAction(a.package, 'stop', t('apps.confirmStop', { prefix: confirmPrefix(a), name: showName(a) })) },
    { sep: true },
    a.disabled
      ? { label: t('apps.menuEnable'), act: () => doAction(a.package, 'enable') }
      : { label: t('apps.menuDisable'), act: () => doAction(a.package, 'disable', t('apps.confirmDisable', { prefix: confirmPrefix(a), name: showName(a) })) },
    { sep: true },
    { label: t('apps.menuClear'), act: () => doAction(a.package, 'clear', t('apps.confirmClear', { prefix: confirmPrefix(a), name: showName(a) })) },
    { label: t('apps.menuUninstall'), act: () => doAction(a.package, 'uninstall', t('apps.confirmUninstall', { prefix: confirmPrefix(a), name: showName(a) })) },
  ])

  if (!selected) {
    return <div className="panel panel-placeholder"><h2>{t('apps.title')}</h2><p className="hint">{t('panel.selectDevice')}</p></div>
  }

  return (
    <div className="panel apps-panel" ref={listRef}>
      <div className="apps-bar">
        <input className="apps-search" placeholder={t('apps.searchPh')} value={keyword} onChange={e => setKeyword(e.target.value)} />
        <label className="apps-sys-chk">
          <input type="checkbox" checked={showSys} onChange={e => setShowSys(e.target.checked)} />
          {t('apps.showSys')}
        </label>
        <span className="hint">{loading ? t('apps.loadingFirst') : t('apps.count', { f: filtered.length, t: apps.length })}</span>
        <span className="spacer" />
        {hint && <span className="hint">{hint}</span>}
        <button className="btn ghost" disabled={loading} onClick={() => load(true)}>{t('common.refresh')}</button>
      </div>
      {err && <p className="error-text">{err}</p>}
      <div className="apps-table-wrap">
        <table className="apps-table">
          <thead>
            <tr>
              {APP_COLS.map((key, i) => th(APP_KEYS[i], t(key), i))}
            </tr>
          </thead>
          <tbody>
            {filtered.map(a => (
              <tr key={a.package}
                className={a.disabled ? 'disabled' : ''}
                onContextMenu={e => { e.preventDefault(); setMenu({ x: e.clientX, y: e.clientY, app: a }) }}
                onClick={() => openInfo(a.package)}
                title={t('apps.rowTip')}>
                <td className="apps-label">{a.label || '—'}{a.system && <span className="apps-tag sys">{t('apps.sysTag')}</span>}</td>
                <td className="mono">{a.package}</td>
                <td className="mono">{a.versionName || '—'}</td>
                <td className="mono num">{a.minSdk || '—'}</td>
                <td className="mono num">{a.targetSdk || '—'}</td>
                <td className="mono num">{fmtSize(storageOf(a))}</td>
                <td>{a.disabled ? <span className="apps-tag off">{t('apps.disabled')}</span> : <span className="apps-tag en">{t('apps.enabled')}</span>}</td>
              </tr>
            ))}
            {!filtered.length && !loading && <tr><td colSpan={7} className="hint apps-empty">{t('apps.noMatch')}</td></tr>}
          </tbody>
        </table>
      </div>

      {/* 右键菜单（对齐 AYA：应用信息/导出安装包/打开/停止/冻结/解冻/清除数据/卸载） */}
      {menu && (
        <div className="ctx-menu" ref={menuRef} style={{ left: menu.x, top: menu.y }}
          onClick={e => e.stopPropagation()}>
          {menuItems(menu.app).map((it, i) => 'sep' in it && it.sep
            ? <div key={i} className="ctx-sep" />
            : <button key={i} className="ctx-item" disabled={busyPkg === menu.app.package}
                onClick={() => (it as { act: () => void }).act()}>
                {(it as { label: string }).label}
              </button>)}
        </div>
      )}

      {/* 详情弹窗（对齐 AYA PackageInfoModal 字段） */}
      {info && (
        <div className="apps-modal-mask" onClick={() => setInfo(null)}>
          <div className="apps-modal" onClick={e => e.stopPropagation()}>
            <h3>{info.label || info.package}</h3>
            <dl>
              <dt>{t('apps.dName')}</dt><dd>{info.label || '—'}</dd>
              <dt>{t('apps.dPackage')}</dt><dd className="mono">{info.package}</dd>
              <dt>{t('common.version')}</dt><dd>{info.versionName || '—'}{info.versionCode ? <span className="hint">（{info.versionCode}）</span> : null}</dd>
              <dt>{t('apps.dType')}</dt><dd>{info.system ? t('apps.dSys') : t('apps.dUser')}</dd>
              <dt>{t('common.status')}</dt><dd>{info.enabled ? t('apps.enabled') : t('apps.disabled')}</dd>
              <dt>{t('apps.dMinSdk')}</dt><dd>{info.minSdk || '—'}</dd>
              <dt>{t('apps.dTargetSdk')}</dt><dd>{info.targetSdk || '—'}</dd>
              <dt>{t('apps.dFirstInstall')}</dt><dd>{info.firstInstall || '—'}</dd>
              <dt>{t('apps.dLastUpdate')}</dt><dd>{info.lastUpdate || info.firstInstall || '—'}</dd>
              <dt>{t('apps.dApkPath')}</dt><dd className="mono">{info.apkPath || '—'}</dd>
            </dl>
            <div className="apps-modal-foot">
              <button className="btn" onClick={() => { doAction(info.package, 'open'); setInfo(null) }}>{t('apps.dLaunch')}</button>
              <button className="btn ghost" onClick={() => setInfo(null)}>{t('common.close')}</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
