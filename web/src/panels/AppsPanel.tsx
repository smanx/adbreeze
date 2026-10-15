import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'
import { useStore } from '../store'
import { Th, useColWidths } from '../components/TableTh'

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
const APP_COLS = ['应用名', '包名', '版本', '最小 SDK', '目标 SDK', '存储占用', '状态']
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
      setHint(`${pkg}: ${r.message || '完成'}`)
      if (action !== 'open') load() // 状态类操作后刷新列表
    } catch (e) { setHint(`${pkg}: 操作失败 - ${(e as Error).message}`) } finally { setBusyPkg('') }
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
    setBusyPkg(app.package); setHint('导出 APK…')
    try {
      const blob = await fetch(`/api/devices/${encodeURIComponent(selected!)}/apps/apk?package=${encodeURIComponent(app.package)}`, {
        headers: { 'X-Auth-Token': useStore.getState().token },
      }).then(r => { if (!r.ok) throw new Error('HTTP ' + r.status); return r.blob() })
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      // AYA 同款命名：包名-版本.apk
      a.download = app.versionName ? `${app.package}-${app.versionName}.apk` : `${app.package}.apk`
      a.click()
      URL.revokeObjectURL(a.href)
      setHint(`${app.package}: APK 已保存到下载`)
    } catch (e) { setHint(`${app.package}: 导出失败 - ${(e as Error).message}`) } finally { setBusyPkg('') }
  }

  const confirmPrefix = (a: AppEntry) => a.system ? '该应用为系统应用。' : ''
  const showName = (a: AppEntry) => a.label || a.package

  const menuItems = (a: AppEntry) => ([
    { label: '应用信息', act: () => openInfo(a.package) },
    { label: '导出安装包', act: () => exportApk(a) },
    { sep: true },
    { label: '打开', act: () => doAction(a.package, 'open') },
    { label: '停止', act: () => doAction(a.package, 'stop', `${confirmPrefix(a)}确定要停止 ${showName(a)} 吗？`) },
    { sep: true },
    a.disabled
      ? { label: '解冻', act: () => doAction(a.package, 'enable') }
      : { label: '冻结', act: () => doAction(a.package, 'disable', `${confirmPrefix(a)}确定要冻结 ${showName(a)} 吗？`) },
    { sep: true },
    { label: '清除数据', act: () => doAction(a.package, 'clear', `${confirmPrefix(a)}确定要清除 ${showName(a)} 的数据吗？`) },
    { label: '卸载', act: () => doAction(a.package, 'uninstall', `${confirmPrefix(a)}确定要卸载 ${showName(a)} 吗？`) },
  ])

  if (!selected) {
    return <div className="panel panel-placeholder"><h2>应用</h2><p className="hint">请先在设备列表中选择设备</p></div>
  }

  return (
    <div className="panel apps-panel" ref={listRef}>
      <div className="apps-bar">
        <input className="apps-search" placeholder="搜索包名/应用名" value={keyword} onChange={e => setKeyword(e.target.value)} />
        <label className="apps-sys-chk">
          <input type="checkbox" checked={showSys} onChange={e => setShowSys(e.target.checked)} />
          系统应用
        </label>
        <span className="hint">{loading ? '加载中…（首次约 7 秒）' : `${filtered.length} / ${apps.length} 个应用`}</span>
        <span className="spacer" />
        {hint && <span className="hint">{hint}</span>}
        <button className="btn ghost" disabled={loading} onClick={() => load(true)}>刷新</button>
      </div>
      {err && <p className="error-text">{err}</p>}
      <div className="apps-table-wrap">
        <table className="apps-table">
          <thead>
            <tr>
              {APP_COLS.map((text, i) => th(APP_KEYS[i], text, i))}
            </tr>
          </thead>
          <tbody>
            {filtered.map(a => (
              <tr key={a.package}
                className={a.disabled ? 'disabled' : ''}
                onContextMenu={e => { e.preventDefault(); setMenu({ x: e.clientX, y: e.clientY, app: a }) }}
                onClick={() => openInfo(a.package)}
                title="右键打开菜单，单击查看详情">
                <td className="apps-label">{a.label || '—'}{a.system && <span className="apps-tag sys">系统</span>}</td>
                <td className="mono">{a.package}</td>
                <td className="mono">{a.versionName || '—'}</td>
                <td className="mono num">{a.minSdk || '—'}</td>
                <td className="mono num">{a.targetSdk || '—'}</td>
                <td className="mono num">{fmtSize(storageOf(a))}</td>
                <td>{a.disabled ? <span className="apps-tag off">已禁用</span> : <span className="apps-tag en">已启用</span>}</td>
              </tr>
            ))}
            {!filtered.length && !loading && <tr><td colSpan={7} className="hint apps-empty">无匹配应用</td></tr>}
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
              <dt>应用名</dt><dd>{info.label || '—'}</dd>
              <dt>包名</dt><dd className="mono">{info.package}</dd>
              <dt>版本</dt><dd>{info.versionName || '—'}{info.versionCode ? <span className="hint">（{info.versionCode}）</span> : null}</dd>
              <dt>类型</dt><dd>{info.system ? '系统应用' : '第三方应用'}</dd>
              <dt>状态</dt><dd>{info.enabled ? '已启用' : '已禁用'}</dd>
              <dt>最小 SDK</dt><dd>{info.minSdk || '—'}</dd>
              <dt>目标 SDK</dt><dd>{info.targetSdk || '—'}</dd>
              <dt>首次安装</dt><dd>{info.firstInstall || '—'}</dd>
              <dt>最后更新</dt><dd>{info.lastUpdate || info.firstInstall || '—'}</dd>
              <dt>APK 路径</dt><dd className="mono">{info.apkPath || '—'}</dd>
            </dl>
            <div className="apps-modal-foot">
              <button className="btn" onClick={() => { doAction(info.package, 'open'); setInfo(null) }}>启动</button>
              <button className="btn ghost" onClick={() => setInfo(null)}>关闭</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
