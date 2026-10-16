import { useState } from 'react'
import { api, apiUrl, authHeaders, type DeviceInfo } from '../api'
import { useStore } from '../store'
import { clipboardGet, clipboardSet } from '../mirror/controller'
import { PreviewModal } from '../components/TableTh'

// 工具面板：一次性 Shell / 批量命令 / APK 安装 / 截图预览 / 剪贴板
export default function ToolsPanel() {
  const devices = useStore(s => s.devices)
  const selected = useStore(s => s.selected)
  const [shellCmd, setShellCmd] = useState('')
  const [shellOut, setShellOut] = useState('')
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [batchCmd, setBatchCmd] = useState('')
  const [batchOut, setBatchOut] = useState('')
  const [hint, setHint] = useState('')
  const [busy, setBusy] = useState(false)
  const [shot, setShot] = useState<{ url: string; filename: string } | null>(null)

  const runShell = async () => {
    if (!selected || !shellCmd.trim()) return
    setBusy(true)
    try {
      const r = await api<{ output: string }>('POST', `/api/devices/${encodeURIComponent(selected)}/shell`, { command: shellCmd.trim() })
      setShellOut(r.output || '(无输出)')
    } catch (e) { setShellOut('[错误] ' + (e as Error).message) } finally { setBusy(false) }
  }

  const runBatch = async () => {
    const cmd = batchCmd.trim()
    const serials = [...checked]
    if (!cmd || !serials.length) { setHint('勾选设备并输入命令'); return }
    setHint(`并行执行于 ${serials.length} 台设备…`)
    setBatchOut('')
    await Promise.all(serials.map(async serial => {
      try {
        const r = await api<{ output: string }>('POST', `/api/devices/${encodeURIComponent(serial)}/shell`, { command: cmd })
        setBatchOut(o => o + `\n==== ${serial} ====\n${r.output || '(无输出)'}`)
      } catch (e) {
        setBatchOut(o => o + `\n==== ${serial} ====\n[错误] ${(e as Error).message}`)
      }
    }))
    setHint('完成')
  }

  const installAPK = async (file: File) => {
    if (!selected) { setHint('请先选择设备'); return }
    setBusy(true); setHint(`正在安装 ${file.name}…`)
    try {
      const body = await file.arrayBuffer()
      const r = await fetch(apiUrl(`/api/devices/${encodeURIComponent(selected)}/install`), {
        method: 'POST', body, headers: authHeaders(),
      })
      const data = await r.json().catch(() => ({}))
      if (!r.ok) throw new Error(data.error || 'HTTP ' + r.status)
      setHint('安装完成：' + (data.message || 'OK'))
    } catch (e) { setHint('安装失败: ' + (e as Error).message) } finally { setBusy(false) }
  }

  const takeShot = async () => {
    if (!selected) { setHint('请先选择设备'); return }
    setBusy(true); setHint('截图…')
    try {
      const blob = await fetch(apiUrl(`/api/devices/${encodeURIComponent(selected)}/screenshot`), { headers: authHeaders() })
        .then(r => { if (!r.ok) throw new Error('HTTP ' + r.status); return r.blob() })
      setShot({
        url: URL.createObjectURL(blob),
        filename: `${selected}-${new Date().toISOString().slice(0, 19).replace(/[T:]/g, '')}.png`,
      })
      setHint('')
    } catch (e) { setHint('截图失败: ' + (e as Error).message) } finally { setBusy(false) }
  }

  const closeShot = () => {
    if (shot) URL.revokeObjectURL(shot.url)
    setShot(null)
  }

  const clipGet = async () => {
    if (!selected) { setHint('请先选择设备'); return }
    try { setHint('设备剪贴板: ' + (await clipboardGet(selected) || '(空)')) } catch (e) { setHint((e as Error).message) }
  }
  const clipSet = async () => {
    if (!selected) { setHint('请先选择设备'); return }
    let text = ''
    try { text = await navigator.clipboard.readText() } catch { /* 无权限 */ }
    if (!text) text = prompt('输入要写入设备剪贴板的文本：') || ''
    if (!text) return
    try { await clipboardSet(selected, text, true); setHint('已写入设备剪贴板并粘贴') } catch (e) { setHint((e as Error).message) }
  }

  const toggle = (serial: string) => {
    setChecked(s => { const n = new Set(s); if (n.has(serial)) n.delete(serial); else n.add(serial); return n })
  }

  return (
    <div className="panel tools-panel">
      <section>
        <h3>一次性 Shell{selected ? `（${selected}）` : ''}</h3>
        <div className="row">
          <input style={{ flex: 1 }} placeholder="如 getprop ro.product.model"
            value={shellCmd} onChange={e => setShellCmd(e.target.value)} onKeyDown={e => e.key === 'Enter' && runShell()} />
          <button className="btn" disabled={busy || !selected} onClick={runShell}>执行</button>
        </div>
        {shellOut && <pre className="tools-out">{shellOut}</pre>}
      </section>

      <section>
        <h3>批量命令</h3>
        <div className="batch-devices">
          {devices.map(d => (
            <label key={d.serial} className={'batch-chk' + (checked.has(d.serial) ? ' on' : '')}>
              <input type="checkbox" checked={checked.has(d.serial)} onChange={() => toggle(d.serial)} />
              {d.serial}
            </label>
          ))}
        </div>
        <div className="row">
          <input style={{ flex: 1 }} placeholder="对勾选设备并行执行的命令"
            value={batchCmd} onChange={e => setBatchCmd(e.target.value)} />
          <button className="btn" onClick={runBatch}>执行</button>
          {hint && <span className="hint">{hint}</span>}
        </div>
        {batchOut && <pre className="tools-out">{batchOut}</pre>}
      </section>

      <section>
        <h3>APK 安装{selected ? `（${selected}）` : ''}</h3>
        <input type="file" accept=".apk" style={{ display: 'none' }} id="apk-input"
          onChange={e => { const f = e.target.files?.[0]; if (f) installAPK(f); e.target.value = '' }} />
        <button className="btn" disabled={!selected || busy} onClick={() => document.getElementById('apk-input')?.click()}>
          选择 APK 并安装
        </button>
      </section>

      <section>
        <h3>截图 / 剪贴板{selected ? `（${selected}）` : ''}</h3>
        <div className="row">
          <button className="btn" disabled={!selected || busy} onClick={takeShot}>截图预览</button>
          <button className="btn ghost" disabled={!selected} onClick={clipGet}>读取剪贴板</button>
          <button className="btn ghost" disabled={!selected} onClick={clipSet}>写入剪贴板</button>
          {hint && <span className="hint">{hint}</span>}
        </div>
      </section>

      {shot && <PreviewModal url={shot.url} title="截图预览" filename={shot.filename} onClose={closeShot} />}
    </div>
  )
}

export type { DeviceInfo }
