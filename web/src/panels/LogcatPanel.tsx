import { useCallback, useEffect, useRef, useState } from 'react'
import { useStore } from '../store'
import { wsUrl } from '../api'
import { useT } from '../i18n'

// Logcat 面板：实时日志流（threadtime 格式）+ 客户端过滤（级别/关键字/Tag）+
// 级别着色 + 暂停/恢复 + 清屏 + 导出。对齐 AYA Logcat 面板功能子集。
interface LogEntry {
  raw: string
  time: string
  pid: string
  tid: string
  level: string
  tag: string
  msg: string
}

// threadtime: MM-DD HH:MM:SS.mmm PID TID LEVEL TAG: msg
const LINE_RE = /^(\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d{3})\s+(\d+)\s+(\d+)\s+([VDIWEF])\s+(\S+?)\s*:\s(.*)$/
const LEVELS = ['V', 'D', 'I', 'W', 'E', 'F'] as const
const LEVEL_NAMES: Record<string, string> = { V: 'Verbose', D: 'Debug', I: 'Info', W: 'Warning', E: 'Error', F: 'Fatal' }
const LEVEL_COLOR: Record<string, string> = {
  V: '#fa541c', // volcano
  D: '#1677ff', // blue
  I: '#52c41a', // green
  W: '#fa8c16', // orange
  E: '#f5222d', // red
  F: '#c41d7f', // magenta
}
const MAX_LINES = 10000

function parseLine(raw: string): LogEntry | null {
  const m = LINE_RE.exec(raw)
  if (!m) return null
  return { raw, time: m[1], pid: m[2], tid: m[3], level: m[4], tag: m[5], msg: m[6] }
}

export default function LogcatPanel() {
  const selected = useStore(s => s.selected)
  const [lines, setLines] = useState<LogEntry[]>([])
  const [connected, setConnected] = useState(false)
  const [status, setStatus] = useState('')
  const [paused, setPaused] = useState(false)
  const [minLevel, setMinLevel] = useState<string>('V')
  const [keyword, setKeyword] = useState('')
  const [tag, setTag] = useState('')
  const [autoScroll, setAutoScroll] = useState(true)
  const tr = useT()

  const wsRef = useRef<WebSocket | null>(null)
  const pausedRef = useRef(false)
  const boxRef = useRef<HTMLDivElement>(null)
  const tailRef = useRef<HTMLDivElement>(null)

  pausedRef.current = paused

  const append = useCallback((raw: string) => {
    if (pausedRef.current) return // 暂停期间直接丢弃（对齐 AYA 主进程级暂停）
    const entry = parseLine(raw) || { raw, time: '', pid: '', tid: '', level: '', tag: '', msg: raw }
    setLines(prev => {
      const next = prev.length >= MAX_LINES ? prev.slice(prev.length - MAX_LINES + 1) : prev
      return [...next, entry]
    })
  }, [])

  // 建立日志流：设备切换或重连时重建
  useEffect(() => {
    if (!selected) return
    setLines([])
    setStatus(tr('logcat.connecting'))
    const ws = new WebSocket(wsUrl(`/ws/logcat/${encodeURIComponent(selected)}`))
    wsRef.current = ws
    ws.onopen = () => { setConnected(true); setStatus('') }
    ws.onmessage = ev => { if (typeof ev.data === 'string') append(ev.data) }
    ws.onclose = ev => {
      if (wsRef.current !== ws) return
      wsRef.current = null
      setConnected(false)
      setStatus(ev.reason || ev.code === 4000 ? tr('logcat.disconnected', { reason: ev.reason || tr('logcat.interruptReason') }) : tr('logcat.disconnectedOffline'))
    }
    return () => { ws.onclose = null; ws.close(); wsRef.current = null; setConnected(false) }
  }, [selected, append, tr])

  // 自动滚动
  useEffect(() => {
    if (autoScroll) tailRef.current?.scrollIntoView({ block: 'end' })
  }, [lines, autoScroll])

  const clear = () => {
    setLines([])
    wsRef.current?.send(JSON.stringify({ type: 'clear' }))
  }

  const exportLog = () => {
    if (!lines.length) return
    const text = lines.map(l => l.raw).join('\n')
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
    a.download = `logcat-${selected}-${new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14)}.txt`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  // 客户端过滤：级别下限 + 关键字（全行匹配）+ Tag
  const filtered = lines.filter(l => {
    if (l.level && LEVELS.indexOf(l.level as (typeof LEVELS)[number]) < LEVELS.indexOf(minLevel as (typeof LEVELS)[number])) return false
    if (tag && !l.tag.toLowerCase().includes(tag.toLowerCase())) return false
    if (keyword && !l.raw.toLowerCase().includes(keyword.toLowerCase())) return false
    return true
  })

  const scrollBox = boxRef.current
  const onScroll = () => {
    if (!scrollBox) return
    setAutoScroll(scrollBox.scrollHeight - scrollBox.scrollTop - scrollBox.clientHeight < 40)
  }

  return (
    <div className="panel logcat-panel">
      <div className="logcat-bar">
        <select value={minLevel} onChange={e => setMinLevel(e.target.value)} title={tr('logcat.minLevel')}>
          {LEVELS.map(l => <option key={l} value={l}>{LEVEL_NAMES[l]}</option>)}
        </select>
        <input className="logcat-filter" placeholder={tr('logcat.keywordPh')} value={keyword} onChange={e => setKeyword(e.target.value)} />
        <input className="logcat-filter" placeholder={tr('logcat.tagPh')} value={tag} onChange={e => setTag(e.target.value)} />
        <span className="spacer" />
        <span className="hint">{connected ? tr('logcat.lines', { f: filtered.length, t: lines.length }) : ''}{status && ` ${status}`}</span>
        <button className="btn ghost" disabled={!connected} onClick={() => setPaused(p => !p)}>{paused ? tr('logcat.resume') : tr('logcat.pause')}</button>
        <button className="btn ghost" onClick={clear}>{tr('common.clear')}</button>
        <button className="btn ghost" disabled={!lines.length} onClick={exportLog}>{tr('common.export')}</button>
      </div>
      {!selected && <p className="hint">{tr('panel.selectDevice')}</p>}
      <div className="logcat-box" ref={boxRef} onScroll={onScroll}>
        {filtered.map((l, i) => (
          <div key={i} className="logcat-line">
            <span className="lv" style={{ color: LEVEL_COLOR[l.level] || 'var(--muted)' }}>{l.level || '·'}</span>
            <span className="tm">{l.time}</span>
            <span className="pid">{l.pid}</span>
            <span className="tg">{l.tag}</span>
            <span className="ms">{l.msg}</span>
          </div>
        ))}
        {!filtered.length && <div className="logcat-empty hint">{selected ? (connected ? tr('logcat.waiting') : status || tr('logcat.notConnected')) : tr('logcat.noDevice')}</div>}
        <div ref={tailRef} />
      </div>
      {!autoScroll && (
        <button className="btn tiny logcat-bottom" onClick={() => tailRef.current?.scrollIntoView({ block: 'end' })}>{tr('logcat.toBottom')}</button>
      )}
    </div>
  )
}
