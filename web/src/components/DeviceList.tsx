import { useEffect, useRef, useState } from 'react'
import { useStore } from '../store'

// 设备列表（PC 侧栏 / 手机抽屉共用）：状态徽标、选中、无线连接（带历史）、断开。
// 掉线的无线设备保留在列表中（offline 灰显），支持一键重连或从列表移除。
export default function DeviceList() {
  const devices = useStore(s => s.devices)
  const selected = useStore(s => s.selected)
  const select = useStore(s => s.select)
  const connectWireless = useStore(s => s.connectWireless)
  const disconnectDevice = useStore(s => s.disconnectDevice)
  const forgetDevice = useStore(s => s.forgetDevice)
  const [addr, setAddr] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [connecting, setConnecting] = useState('') // 正在重连的 serial
  const histRef = useRef<HTMLDataListElement>(null)

  useEffect(() => { renderHistory() }, [])

  const renderHistory = () => {
    const list = JSON.parse(localStorage.getItem('ADBreeze-adb-addresses') || '[]') as string[]
    if (histRef.current) {
      histRef.current.innerHTML = list.map(a => `<option value="${a.replace(/"/g, '&quot;')}"></option>`).join('')
    }
  }

  const remember = (address: string) => {
    const list = (JSON.parse(localStorage.getItem('ADBreeze-adb-addresses') || '[]') as string[])
      .filter(a => a !== address)
    list.unshift(address)
    localStorage.setItem('ADBreeze-adb-addresses', JSON.stringify(list.slice(0, 10)))
    renderHistory()
  }

  const connect = async () => {
    const address = addr.trim()
    if (!address) return
    setBusy(true); setError('')
    try {
      await connectWireless(address)
      remember(address)
      setAddr('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const disconnect = async (serial: string) => {
    try { await disconnectDevice(serial) } catch (e) { setError((e as Error).message) }
  }

  const reconnect = async (serial: string) => {
    setConnecting(serial); setError('')
    try {
      await connectWireless(serial)
      remember(serial)
    } catch (e) {
      setError(`${serial}: ${(e as Error).message}`)
    } finally {
      setConnecting('')
    }
  }

  const forget = async (serial: string) => {
    try { await forgetDevice(serial) } catch (e) { setError((e as Error).message) }
  }

  return (
    <div className="device-list">
      <div className="device-items">
        {devices.length === 0 && <div className="hint">无设备</div>}
        {devices.map(d => {
          const offline = d.state !== 'device'
          return (
            <div
              key={d.serial}
              className={'device-item' + (d.serial === selected ? ' active' : '') + (offline ? ' off' : '')}
              onClick={() => select(d.serial)}
            >
              <div className="device-main">
                <span className="device-serial">{d.serial}</span>
                <span className={'state-badge ' + d.state}>{offline ? '已断开' : d.state}</span>
              </div>
              {offline ? (
                <div className="device-ops">
                  <button
                    className="btn tiny"
                    disabled={connecting === d.serial}
                    onClick={e => { e.stopPropagation(); reconnect(d.serial) }}
                  >{connecting === d.serial ? '重连中…' : '重连'}</button>
                  <button
                    className="btn tiny ghost"
                    onClick={e => { e.stopPropagation(); forget(d.serial) }}
                  >移除</button>
                </div>
              ) : (
                <button
                  className="btn tiny danger"
                  onClick={e => { e.stopPropagation(); disconnect(d.serial) }}
                >断开</button>
              )}
            </div>
          )
        })}
      </div>
      <div className="connect-form">
        <input
          list="addr-history"
          placeholder="192.168.1.20:5555"
          value={addr}
          onChange={e => { setAddr(e.target.value); setError('') }}
          onKeyDown={e => e.key === 'Enter' && connect()}
        />
        <datalist id="addr-history" ref={histRef} />
        <button className="btn" disabled={busy} onClick={connect}>{busy ? '连接中…' : '连接设备'}</button>
        {error && <p className="hint error-text">{error}</p>}
      </div>
    </div>
  )
}
