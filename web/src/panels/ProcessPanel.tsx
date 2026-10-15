import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'
import { useStore } from '../store'
import { Th, useColWidths, type SortSpec } from '../components/TableTh'

// 进程面板：top -b -n 1 采样（CPU/MEM），5 秒轮询（仅面板激活时），支持过滤与停止（对齐 AYA Process）。
// 列：PID/用户/CPU%/内存/进程名，全部支持升降序排序（与应用面板一致），列宽可拖拽调整。
interface ProcEntry {
  pid: string
  user: string
  cpu: number
  mem: string
  name: string
  args?: string
}

type ProcSortKey = 'pid' | 'user' | 'cpu' | 'mem' | 'name'
const PROC_KEYS: ProcSortKey[] = ['pid', 'user', 'cpu', 'mem', 'name']
const PROC_COLS = ['PID', '用户', 'CPU%', '内存', '进程名']

export default function ProcessPanel() {
  const selected = useStore(s => s.selected)
  const [procs, setProcs] = useState<ProcEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const [keyword, setKeyword] = useState('')
  const [onlyApp, setOnlyApp] = useState(false)
  const [selectedPid, setSelectedPid] = useState('')
  const [hint, setHint] = useState('')
  const [sort, setSort] = useState<SortSpec<ProcSortKey>>({ key: 'cpu', asc: false })
  const [widths, setWidth] = useColWidths('adbreeze-proc-colwidths', PROC_COLS.length)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const aliveRef = useRef(true)

  const load = useCallback(async () => {
    if (!selected) return
    setLoading(true)
    try {
      setProcs(await api<ProcEntry[]>('GET', `/api/devices/${encodeURIComponent(selected)}/processes`))
      setErr('')
    } catch (e) { setErr((e as Error).message) } finally { setLoading(false) }
  }, [selected])

  useEffect(() => {
    setProcs([]); setSelectedPid(''); setErr('')
    aliveRef.current = true
    const tick = async () => {
      await load()
      if (aliveRef.current) timerRef.current = setTimeout(tick, 5000)
    }
    tick()
    return () => { aliveRef.current = false; if (timerRef.current) clearTimeout(timerRef.current) }
  }, [load])

  const kill = async () => {
    const p = procs.find(x => x.pid === selectedPid)
    if (!p) return
    const isApp = /^u0_a/.test(p.user) || /^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$/.test(p.name)
    const msg = p.name.includes('.') || isApp
      ? `停止进程 ${p.name}（PID ${p.pid}）？\n将执行 am force-stop。`
      : `kill -9 PID ${p.pid}（${p.name}）？\n非应用进程被强杀可能导致系统不稳定！`
    if (!confirm(msg)) return
    setHint('')
    try {
      const r = await api<{ message: string }>('POST', `/api/devices/${encodeURIComponent(selected!)}/processes/kill`, {
        pid: p.pid, package: isApp ? p.name : '',
      })
      setHint(r.message || '完成')
      setSelectedPid('')
      load()
    } catch (e) { setHint('操作失败: ' + (e as Error).message) }
  }

  const clickSort = (k: ProcSortKey) =>
    setSort(s => (s.key === k ? { key: k, asc: !s.asc } : { key: k, asc: true }))

  // 内存列原文（30M/504K/1.2G）转字节数用于排序；无单位视为字节
  const memBytes = (m: string): number => {
    const u: Record<string, number> = { K: 1 << 10, M: 1 << 20, G: 1 << 30 }
    const mt = /^([\d.]+)\s*([KMGT]?)/i.exec(m.trim())
    if (!mt) return 0
    return parseFloat(mt[1]) * (u[(mt[2] || '').toUpperCase()] || 1)
  }

  const filtered = useMemo(() => {
    const list = procs.filter(p => {
      if (onlyApp && !p.name.includes('.')) return false
      const kw = keyword.trim().toLowerCase()
      return !kw || p.name.toLowerCase().includes(kw) || p.pid.includes(kw) || p.user.toLowerCase().includes(kw)
    })
    const dir = sort.asc ? 1 : -1
    return [...list].sort((a, b) => {
      switch (sort.key) {
        case 'pid': return dir * ((parseInt(a.pid, 10) || 0) - (parseInt(b.pid, 10) || 0))
        case 'user': return dir * a.user.localeCompare(b.user)
        case 'cpu': return dir * (a.cpu - b.cpu)
        case 'mem': return dir * (memBytes(a.mem) - memBytes(b.mem))
        case 'name': return dir * a.name.localeCompare(b.name)
      }
    })
  }, [procs, keyword, onlyApp, sort])

  if (!selected) {
    return <div className="panel panel-placeholder"><h2>进程</h2><p className="hint">请先在设备列表中选择设备</p></div>
  }

  return (
    <div className="panel proc-panel">
      <div className="proc-bar">
        <input className="proc-search" placeholder="搜索进程/PID/用户" value={keyword} onChange={e => setKeyword(e.target.value)} />
        <label className="proc-app-chk">
          <input type="checkbox" checked={onlyApp} onChange={e => setOnlyApp(e.target.checked)} />
          仅应用进程
        </label>
        <span className="hint">{loading ? '采样中…' : `${filtered.length} / ${procs.length} 个进程`}</span>
        <span className="spacer" />
        {hint && <span className="hint">{hint}</span>}
        <button className="btn tiny danger" disabled={!selectedPid} onClick={kill}>停止</button>
        <button className="btn ghost" onClick={load}>刷新</button>
      </div>
      {err && <p className="error-text">{err}</p>}
      <div className="proc-table-wrap">
        <table className="proc-table">
          <thead>
            <tr>
              {PROC_COLS.map((text, i) => (
                <Th key={text} colKey={PROC_KEYS[i]} text={text}
                  sort={sort} onSort={clickSort}
                  width={widths[i] || undefined} onResize={w => setWidth(i, w)}
                  align={i === 2 || i === 3 ? 'right' : 'left'} />
              ))}
            </tr>
          </thead>
          <tbody>
            {filtered.map(p => (
              <tr key={p.pid} className={selectedPid === p.pid ? 'active' : ''}
                onClick={() => setSelectedPid(p.pid === selectedPid ? '' : p.pid)}>
                <td className="mono">{p.pid}</td>
                <td className="mono">{p.user}</td>
                <td className={'mono num' + (p.cpu >= 10 ? ' hot' : '')}>{p.cpu.toFixed(1)}</td>
                <td className="mono num">{p.mem}</td>
                <td className="mono">{p.name}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {!filtered.length && <div className="hint proc-empty">{loading ? '采样中…' : '无匹配进程'}</div>}
      </div>
    </div>
  )
}
