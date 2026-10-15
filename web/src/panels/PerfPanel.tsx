import { useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { useStore } from '../store'

// 性能面板：CPU（总/每核+频率）、内存、FPS 折线图 + 电池/温度/uptime 工具栏（对齐 AYA Performance）。
// 1.5 秒轮询（服务端一次采样约 1.2s），仅面板激活时采集；图表为自绘 canvas，保留 60 点。
interface Battery {
  level: number; status: number; temperature: number; voltage: number
  acPowered: boolean; usbPowered: boolean
}
interface PerfSample {
  cpuTotal: number
  cpuLoads: number[]
  cpuFreqs: number[]
  cpuTemp: number
  memUsed: number // KB
  memTotal: number // KB
  battery: Battery
  fps: number
  topApp: string
  uptime: number // 秒
  ts: number
}

const WINDOW = 60 // 保留 60 点
const POLL_MS = 1500

// 内存显示：<1GB 用 MB，≥1GB 用 GB
function fmtMem(kb: number): string {
  if (kb >= 1024 * 1024) return (kb / 1024 / 1024).toFixed(1) + ' GB'
  return (kb / 1024).toFixed(0) + ' MB'
}

function fmtUptime(sec: number): string {
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = Math.floor(sec % 60)
  return `${d}:${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

export default function PerfPanel() {
  const selected = useStore(s => s.selected)
  const [latest, setLatest] = useState<PerfSample | null>(null)
  const [err, setErr] = useState('')
  const [running, setRunning] = useState(true)

  const cpuHist = useRef<number[]>([])
  const memHist = useRef<number[]>([])
  const fpsHist = useRef<number[]>([])
  const perCoreHist = useRef<number[][]>([])
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const aliveRef = useRef(true)
  const runningRef = useRef(true)
  runningRef.current = running

  useEffect(() => {
    cpuHist.current = []; memHist.current = []; fpsHist.current = []; perCoreHist.current = []
    setLatest(null); setErr('')
    aliveRef.current = true
    const tick = async () => {
      if (runningRef.current) {
        try {
          const s = await api<PerfSample>('GET', `/api/devices/${encodeURIComponent(selected!)}/perf`)
          cpuHist.current.push(s.cpuTotal)
          if (cpuHist.current.length > WINDOW) cpuHist.current.shift()
          const memPct = s.memTotal ? s.memUsed / s.memTotal * 100 : 0
          memHist.current.push(memPct)
          if (memHist.current.length > WINDOW) memHist.current.shift()
          fpsHist.current.push(s.fps)
          if (fpsHist.current.length > WINDOW) fpsHist.current.shift()
          // 每核窗口
          s.cpuLoads.forEach((v, i) => {
            if (!perCoreHist.current[i]) perCoreHist.current[i] = []
            perCoreHist.current[i].push(v)
            if (perCoreHist.current[i].length > WINDOW) perCoreHist.current[i].shift()
          })
          setLatest(s)
          setErr('')
        } catch (e) { setErr((e as Error).message) }
      }
      if (aliveRef.current) timerRef.current = setTimeout(tick, POLL_MS)
    }
    tick()
    return () => { aliveRef.current = false; if (timerRef.current) clearTimeout(timerRef.current) }
  }, [selected])

  if (!selected) {
    return <div className="panel panel-placeholder"><h2>性能</h2><p className="hint">请先在设备列表中选择设备</p></div>
  }

  const b = latest?.battery
  const memPct = latest && latest.memTotal ? Math.round(latest.memUsed / latest.memTotal * 100) : 0

  return (
    <div className="panel perf-panel">
      <div className="perf-bar">
        <button className="btn ghost" onClick={() => setRunning(r => !r)}>{running ? '暂停' : '继续'}</button>
        {err && <span className="error-text">{err}</span>}
        <span className="spacer" />
        <span className="perf-batt" title={`电压 ${(b?.voltage || 0) / 1000}V · 温度 ${b?.temperature}°C`}>
          <span className={'batt-icon' + (b && b.level <= 20 ? ' low' : '')}>
            <span className="batt-level" style={{ width: `${b?.level || 0}%` }} />
          </span>
          {b ? `${b.level}% ${(b.acPowered || b.usbPowered) ? '⚡' : ''} ${b.temperature}°C` : ''}
        </span>
        <span className="hint">{latest ? `Uptime ${fmtUptime(latest.uptime)}` : ''}</span>
      </div>

      <div className="perf-grid">
        <ChartCard
          title={latest ? `CPU ${latest.cpuTemp}°C` : 'CPU'}
          value={latest ? `${latest.cpuTotal}` : ''}
          color="#52c41a" max={100} unit="%"
          data={cpuHist.current}
        />
        <ChartCard
          title={latest ? `内存 ${fmtMem(latest.memUsed)} / ${fmtMem(latest.memTotal)}` : '内存'}
          value={latest ? `${memPct}` : ''}
          color="#722ed1" max={100} unit="%"
          data={memHist.current}
        />
        <ChartCard
          title={latest?.topApp ? `FPS ${latest.topApp}` : 'FPS'}
          value={latest ? `${latest.fps}` : ''}
          color="#fa8c16" max={120} unit=""
          data={fpsHist.current}
        />
      </div>

      <div className="perf-grid">
        {latest?.cpuLoads.map((_, i) => (
          <ChartCard
            key={i}
            title={`CPU${i}${latest.cpuFreqs[i] ? ` ${latest.cpuFreqs[i]}MHz` : ''}`}
            value={latest.cpuLoads[i] !== undefined ? `${latest.cpuLoads[i]}` : ''}
            color="#52c41a" max={100} unit="%" small
            data={perCoreHist.current[i] || []}
          />
        ))}
      </div>
    </div>
  )
}

// ChartCard 折线图卡片：canvas 自绘，data 变化时重绘（无第三方依赖）。
function ChartCard({ title, value, data, color, max, unit, small }: {
  title: string; value: string; data: number[]
  color: string; max: number; unit: string; small?: boolean
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const cv = canvasRef.current
    if (!cv) return
    const dpr = window.devicePixelRatio || 1
    const w = cv.clientWidth, h = cv.clientHeight
    if (cv.width !== w * dpr || cv.height !== h * dpr) {
      cv.width = w * dpr; cv.height = h * dpr
    }
    const ctx = cv.getContext('2d')!
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)

    // 网格线（25/50/75%）
    ctx.strokeStyle = 'rgba(128,128,128,0.15)'
    ctx.lineWidth = 1
    for (const fy of [0.25, 0.5, 0.75]) {
      ctx.beginPath()
      ctx.moveTo(0, h * fy)
      ctx.lineTo(w, h * fy)
      ctx.stroke()
    }
    if (data.length < 2) return
    // 折线（右端为最新值）
    const step = w / (WINDOW - 1)
    ctx.beginPath()
    data.forEach((v, i) => {
      const x = w - (data.length - 1 - i) * step
      const y = h - Math.min(v / max, 1) * (h - 2) - 1
      if (i === 0) ctx.moveTo(x, y)
      else ctx.lineTo(x, y)
    })
    ctx.strokeStyle = color
    ctx.lineWidth = 1.5
    ctx.stroke()
    // 填充
    ctx.lineTo(w, h)
    ctx.lineTo(w - (data.length - 1) * step, h)
    ctx.closePath()
    ctx.fillStyle = color + '22'
    ctx.fill()
  }, [data.slice().join(','), color, max]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className={'perf-card' + (small ? ' small' : '')}>
      <div className="perf-card-head">
        <span className="perf-title" title={title}>{title}</span>
        <span className="perf-value" style={{ color }}>{value}{unit && value ? unit : ''}</span>
      </div>
      <canvas ref={canvasRef} className="perf-canvas" />
    </div>
  )
}
