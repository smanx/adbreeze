import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'
import { useStore } from '../store'
import { Th, useColWidths, type SortSpec } from '../components/TableTh'
import { useT, type MsgKey } from '../i18n'

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
const PROC_COLS: MsgKey[] = ['process.colPid', 'process.colUser', 'process.colCpu', 'process.colMem', 'process.colName']

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
  const t = useT()

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
      ? t('process.confirmStopApp', { name: p.name, pid: p.pid })
      : t('process.confirmKill', { pid: p.pid, name: p.name })
    if (!confirm(msg)) return
    setHint('')
    try {
      const r = await api<{ message: string }>('POST', `/api/devices/${encodeURIComponent(selected!)}/processes/kill`, {
        pid: p.pid, package: isApp ? p.name : '',
      })
      setHint(r.message || t('common.done'))
      setSelectedPid('')
      load()
    } catch (e) { setHint(t('process.opFailed') + (e as Error).message) }
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
    return <div className="panel panel-placeholder"><h2>{t('process.title')}</h2><p className="hint">{t('panel.selectDevice')}</p></div>
  }

  return (
    <div className="panel proc-panel">
      <div className="proc-bar">
        <input className="proc-search" placeholder={t('process.searchPh')} value={keyword} onChange={e => setKeyword(e.target.value)} />
        <label className="proc-app-chk">
          <input type="checkbox" checked={onlyApp} onChange={e => setOnlyApp(e.target.checked)} />
          {t('process.onlyApp')}
        </label>
        <span className="hint">{loading ? t('process.sampling') : t('process.count', { f: filtered.length, t: procs.length })}</span>
        <span className="spacer" />
        {hint && <span className="hint">{hint}</span>}
        <button className="btn tiny danger" disabled={!selectedPid} onClick={kill}>{t('process.stop')}</button>
        <button className="btn ghost" onClick={load}>{t('common.refresh')}</button>
      </div>
      {err && <p className="error-text">{err}</p>}
      <div className="proc-table-wrap">
        <table className="proc-table">
          <thead>
            <tr>
              {PROC_COLS.map((key, i) => (
                <Th key={key} colKey={PROC_KEYS[i]} text={t(key)}
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
        {!filtered.length && <div className="hint proc-empty">{loading ? t('process.sampling') : t('process.noMatch')}</div>}
      </div>
    </div>
  )
}
