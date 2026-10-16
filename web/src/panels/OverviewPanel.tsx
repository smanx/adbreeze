import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'
import { useStore } from '../store'
import { useT, type MsgKey } from '../i18n'

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

// 电池状态码 → 文案（Android BatteryManager）
const STATUS_KEYS: Record<number, MsgKey> = {
  2: 'overview.battCharging', 3: 'overview.battDischarging', 4: 'overview.battNotCharging',
  5: 'overview.battFull', 1: 'common.unknown',
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
  const t = useT()

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
    return <div className="panel panel-placeholder"><h2>{t('overview.title')}</h2><p className="hint">{t('panel.selectDevice')}</p></div>
  }

  const info = data?.info
  const memUsed = info && info.memTotalKB > info.memAvailKB ? info.memTotalKB - info.memAvailKB : 0

  return (
    <div className="panel overview-panel">
      <div className="overview-head">
        <h2>{data ? t('overview.withSerial', { serial: data.serial }) : t('overview.title')}</h2>
        <span className="spacer" />
        {copied && <span className="hint">{t('overview.copied', { label: copied })}</span>}
        <button className="btn ghost" disabled={loading} onClick={load}>{loading ? t('common.loading') : t('common.refresh')}</button>
      </div>
      {err && <p className="error-text">{err}</p>}
      {!info && !err && <p className="hint">{t('common.loading')}</p>}
      {info && (
        <>
          <section className="ov-grid">
            <Item label={t('overview.deviceName')} value={info.marketName} copy={copy} copied={copied} />
            <Item label={t('overview.brand')} value={info.brand} copy={copy} copied={copied} />
            <Item label={t('overview.model')} value={info.model} copy={copy} copied={copied} />
            <Item label={t('overview.serial')} value={info.serialno} copy={copy} copied={copied} />
            <Item label={t('overview.android')} value={info.android ? `${info.android}${t('overview.apiSuffix', { sdk: info.sdk })}` : ''} copy={copy} copied={copied} />
            <Item label={t('overview.kernel')} value={info.kernel} copy={copy} copied={copied} />
            <Item label={t('overview.soc')} value={info.board ? `${info.board}${t('overview.coresSuffix', { cores: info.cpuCores, abi: info.abi })}` : ''} copy={copy} copied={copied} />
            <Item label={t('overview.build')} value={info.incremental} copy={copy} copied={copied} />
            <Item label={t('overview.fontScale')} value={info.fontScale} copy={copy} copied={copied} />
          </section>

          <section className="ov-grid">
            <Item label={t('overview.memory')} value={info.memTotalKB ? `${fmtKB(memUsed)} / ${fmtKB(info.memTotalKB)}` : ''} copy={copy} copied={copied} />
            <Item label={t('overview.storage')} value={info.storageTotalKB ? `${fmtKB(info.storageUsedKB)} / ${fmtKB(info.storageTotalKB)}` : ''} copy={copy} copied={copied} />
            <Item label={t('overview.physSize')} value={info.physSize ? `${info.physSize}${t('overview.dpiSuffix', { dpi: info.physDensity || '—' })}` : ''} copy={copy} copied={copied} />
            <Item label={t('overview.overSize')} value={info.overSize ? `${info.overSize}${t('overview.dpiSuffix', { dpi: info.overDensity || '—' })}` : t('overview.notOverridden')} copy={copy} copied={copied} />
            <Item label={t('overview.wifi')} value={info.wifi} copy={copy} copied={copied} />
            <Item label={t('overview.ip')} value={info.ip} copy={copy} copied={copied} />
            <Item label={t('overview.mac')} value={info.mac} copy={copy} copied={copied} />
            <Item label={t('overview.cpuTemp')} value={info.cpuTemp ? `${info.cpuTemp.toFixed(1)} °C` : ''} copy={copy} copied={copied} />
          </section>

          <section className="ov-grid">
            <Item label={t('overview.battLevel')} value={info.battery.level ? `${info.battery.level}%` : ''} copy={copy} copied={copied} />
            <Item label={t('overview.battStatus')} value={STATUS_KEYS[info.battery.status] ? t(STATUS_KEYS[info.battery.status]) : String(info.battery.status || '')} copy={copy} copied={copied} />
            <Item label={t('overview.battTemp')} value={info.battery.temperature ? `${info.battery.temperature.toFixed(1)} °C` : ''} copy={copy} copied={copied} />
            <Item label={t('overview.battVolt')} value={info.battery.voltage ? `${(info.battery.voltage / 1000).toFixed(2)} V` : ''} copy={copy} copied={copied} />
            <Item label={t('overview.battType')} value={info.battery.technology} copy={copy} copied={copied} />
            <Item label={t('overview.powerSource')} value={(() => {
              const b = info.battery
              if (b.acPowered) return t('overview.ac')
              if (b.usbPowered) return 'USB'
              if (b.wirelessPowered) return t('overview.wireless')
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
  const t = useT()
  return (
    <div className={'ov-item' + (copied === label ? ' copied' : '')}
      onClick={() => copy(label, value)} title={value ? t('overview.copyTip', { value }) : ''}>
      <span className="ov-label">{label}</span>
      <span className="ov-value">{value || '—'}</span>
    </div>
  )
}
