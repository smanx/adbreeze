import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'
import { useStore } from '../store'

// 概览面板：设备信息总览（对齐 AYA Overview），一次性拉取 + 手动刷新，点击条目复制值。
interface Battery {
  level: number; status: number; temperature: number; voltage: number
  technology: string; acPowered: boolean; usbPowered: boolean; wirelessPowered: boolean
  chargeCounter: number
}
interface OverviewInfo {
  marketName: string; brand: string; model: string; serialno: string
  android: string; sdk: string; board: string; abi: string; incremental: string
  kernel: string; cpuCores: number
  memTotalKB: number; memAvailKB: number
  storageUsedKB: number; storageTotalKB: number
  physSize: string; physDensity: string; overSize: string; overDensity: string
  fontScale: string; wifi: string; ip: string; mac: string
  cpuTemp: number; battery: Battery
}
interface OverviewResp {
  serial: string
  info: OverviewInfo
}

const STATUS_TEXT: Record<number, string> = {
  2: '充电中', 3: '放电中', 4: '未充电', 5: '已充满', 1: '未知',
}

function fmtKB(kb: number): string {
  if (!kb) return '—'
  if (kb >= 1024 * 1024) return (kb / 1024 / 1024).toFixed(1) + ' GB'
  if (kb >= 1024) return (kb / 1024).toFixed(0) + ' MB'
  return kb + ' KB'
}

export default function OverviewPanel() {
  const selected = useStore(s => s.selected)
  const [data, setData] = useState<OverviewResp | null>(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const [copied, setCopied] = useState('')

  const load = useCallback(async () => {
    if (!selected) return
    setLoading(true); setErr('')
    try {
      setData(await api<OverviewResp>('GET', `/api/devices/${encodeURIComponent(selected)}/overview`))
    } catch (e) { setErr((e as Error).message) } finally { setLoading(false) }
  }, [selected])

  useEffect(() => { setData(null); load() }, [load])

  const copy = (label: string, value: string) => {
    if (!value || value === '—') return
    navigator.clipboard.writeText(value).then(() => {
      setCopied(label); setTimeout(() => setCopied(''), 1200)
    }).catch(() => { /* 无剪贴板权限 */ })
  }

  if (!selected) {
    return <div className="panel panel-placeholder"><h2>概览</h2><p className="hint">请先在设备列表中选择设备</p></div>
  }

  const info = data?.info
  const memUsed = info && info.memTotalKB > info.memAvailKB ? info.memTotalKB - info.memAvailKB : 0

  return (
    <div className="panel overview-panel">
      <div className="overview-head">
        <h2>概览{data ? `（${data.serial}）` : ''}</h2>
        <span className="spacer" />
        {copied && <span className="hint">已复制「{copied}」</span>}
        <button className="btn ghost" disabled={loading} onClick={load}>{loading ? '加载中…' : '刷新'}</button>
      </div>
      {err && <p className="error-text">{err}</p>}
      {!info && !err && <p className="hint">加载中…</p>}
      {info && (
        <>
          <section className="ov-grid">
            <Item label="设备名称" value={info.marketName} copy={copy} copied={copied} />
            <Item label="品牌" value={info.brand} copy={copy} copied={copied} />
            <Item label="型号" value={info.model} copy={copy} copied={copied} />
            <Item label="序列号" value={info.serialno} copy={copy} copied={copied} />
            <Item label="Android 版本" value={info.android ? `${info.android}（API ${info.sdk}）` : ''} copy={copy} copied={copied} />
            <Item label="内核版本" value={info.kernel} copy={copy} copied={copied} />
            <Item label="处理器" value={info.board ? `${info.board}（${info.cpuCores} 核，${info.abi}）` : ''} copy={copy} copied={copied} />
            <Item label="构建版本" value={info.incremental} copy={copy} copied={copied} />
            <Item label="字体缩放" value={info.fontScale} copy={copy} copied={copied} />
          </section>

          <section className="ov-grid">
            <Item label="内存" value={info.memTotalKB ? `${fmtKB(memUsed)} / ${fmtKB(info.memTotalKB)}` : ''} copy={copy} copied={copied} />
            <Item label="存储" value={info.storageTotalKB ? `${fmtKB(info.storageUsedKB)} / ${fmtKB(info.storageTotalKB)}` : ''} copy={copy} copied={copied} />
            <Item label="物理分辨率" value={info.physSize ? `${info.physSize}（${info.physDensity || '—'} dpi）` : ''} copy={copy} copied={copied} />
            <Item label="显示分辨率" value={info.overSize ? `${info.overSize}（${info.overDensity || '—'} dpi）` : '未覆盖'} copy={copy} copied={copied} />
            <Item label="Wi-Fi" value={info.wifi} copy={copy} copied={copied} />
            <Item label="IP 地址" value={info.ip} copy={copy} copied={copied} />
            <Item label="MAC 地址" value={info.mac} copy={copy} copied={copied} />
            <Item label="CPU 温度" value={info.cpuTemp ? `${info.cpuTemp.toFixed(1)} °C` : ''} copy={copy} copied={copied} />
          </section>

          <section className="ov-grid">
            <Item label="电池电量" value={info.battery.level ? `${info.battery.level}%` : ''} copy={copy} copied={copied} />
            <Item label="电池状态" value={STATUS_TEXT[info.battery.status] || String(info.battery.status || '')} copy={copy} copied={copied} />
            <Item label="电池温度" value={info.battery.temperature ? `${info.battery.temperature.toFixed(1)} °C` : ''} copy={copy} copied={copied} />
            <Item label="电池电压" value={info.battery.voltage ? `${(info.battery.voltage / 1000).toFixed(2)} V` : ''} copy={copy} copied={copied} />
            <Item label="电池类型" value={info.battery.technology} copy={copy} copied={copied} />
            <Item label="充电方式" value={(() => {
              const b = info.battery
              if (b.acPowered) return '交流电源'
              if (b.usbPowered) return 'USB'
              if (b.wirelessPowered) return '无线'
              return ''
            })()} copy={copy} copied={copied} />
          </section>
        </>
      )}
    </div>
  )
}

function Item({ label, value, copy, copied }: {
  label: string; value: string
  copy: (label: string, value: string) => void; copied: string
}) {
  return (
    <div className={'ov-item' + (copied === label ? ' copied' : '')}
      onClick={() => copy(label, value)} title={value ? `点击复制：${value}` : ''}>
      <span className="ov-label">{label}</span>
      <span className="ov-value">{value || '—'}</span>
    </div>
  )
}
