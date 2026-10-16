import { useState } from 'react'
import { api, apiUrl, authHeaders, installApk, type DeviceInfo } from '../api'
import { useStore } from '../store'
import { clipboardGet, clipboardSet } from '../mirror/controller'
import { PreviewModal } from '../components/TableTh'
import { useT } from '../i18n'

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
  const t = useT()

  const serialSuffix = selected ? t('tools.serialSuffix', { serial: selected }) : ''

  const runShell = async () => {
    if (!selected || !shellCmd.trim()) return
    setBusy(true)
    try {
      const r = await api<{ output: string }>('POST', `/api/devices/${encodeURIComponent(selected)}/shell`, { command: shellCmd.trim() })
      setShellOut(r.output || t('common.noOutput'))
    } catch (e) { setShellOut(t('common.errorPrefix') + (e as Error).message) } finally { setBusy(false) }
  }

  const runBatch = async () => {
    const cmd = batchCmd.trim()
    const serials = [...checked]
    if (!cmd || !serials.length) { setHint(t('tools.batchNeedSel')); return }
    setHint(t('tools.batchRunning', { n: serials.length }))
    setBatchOut('')
    await Promise.all(serials.map(async serial => {
      try {
        const r = await api<{ output: string }>('POST', `/api/devices/${encodeURIComponent(serial)}/shell`, { command: cmd })
        setBatchOut(o => o + `\n${t('tools.batchHeader', { serial })}\n${r.output || t('common.noOutput')}`)
      } catch (e) {
        setBatchOut(o => o + `\n${t('tools.batchHeader', { serial })}\n${t('common.errorPrefix')}${(e as Error).message}`)
      }
    }))
    setHint(t('common.done'))
  }

  const installAPK = async (file: File) => {
    if (!selected) { setHint(t('panel.selectDeviceShort')); return }
    setBusy(true); setHint(t('tools.installing', { name: file.name }))
    try {
      const r = await installApk(selected, file)
      setHint(t('tools.installDone') + (r.message || 'OK'))
    } catch (e) { setHint(t('tools.installFailed') + (e as Error).message) } finally { setBusy(false) }
  }

  const takeShot = async () => {
    if (!selected) { setHint(t('panel.selectDeviceShort')); return }
    setBusy(true); setHint(t('tools.capturing'))
    try {
      const blob = await fetch(apiUrl(`/api/devices/${encodeURIComponent(selected)}/screenshot`), { headers: authHeaders() })
        .then(r => { if (!r.ok) throw new Error('HTTP ' + r.status); return r.blob() })
      setShot({
        url: URL.createObjectURL(blob),
        filename: `${selected}-${new Date().toISOString().slice(0, 19).replace(/[T:]/g, '')}.png`,
      })
      setHint('')
    } catch (e) { setHint(t('tools.shotFailed') + (e as Error).message) } finally { setBusy(false) }
  }

  const closeShot = () => {
    if (shot) URL.revokeObjectURL(shot.url)
    setShot(null)
  }

  const clipGet = async () => {
    if (!selected) { setHint(t('panel.selectDeviceShort')); return }
    try { setHint(t('tools.clipDevice') + (await clipboardGet(selected) || t('tools.clipEmpty'))) } catch (e) { setHint((e as Error).message) }
  }
  const clipSet = async () => {
    if (!selected) { setHint(t('panel.selectDeviceShort')); return }
    let text = ''
    try { text = await navigator.clipboard.readText() } catch { /* 无权限 */ }
    if (!text) text = prompt(t('mirror.clipPrompt')) || ''
    if (!text) return
    try { await clipboardSet(selected, text, true); setHint(t('mirror.clipWritten')) } catch (e) { setHint((e as Error).message) }
  }

  const toggle = (serial: string) => {
    setChecked(s => { const n = new Set(s); if (n.has(serial)) n.delete(serial); else n.add(serial); return n })
  }

  return (
    <div className="panel tools-panel">
      <section>
        <h3>{t('tools.shell', { serial: serialSuffix })}</h3>
        <div className="row">
          <input style={{ flex: 1 }} placeholder={t('tools.shellPh')}
            value={shellCmd} onChange={e => setShellCmd(e.target.value)} onKeyDown={e => e.key === 'Enter' && runShell()} />
          <button className="btn" disabled={busy || !selected} onClick={runShell}>{t('common.run')}</button>
        </div>
        {shellOut && <pre className="tools-out">{shellOut}</pre>}
      </section>

      <section>
        <h3>{t('tools.batch')}</h3>
        <div className="batch-devices">
          {devices.map(d => (
            <label key={d.serial} className={'batch-chk' + (checked.has(d.serial) ? ' on' : '')}>
              <input type="checkbox" checked={checked.has(d.serial)} onChange={() => toggle(d.serial)} />
              {d.serial}
            </label>
          ))}
        </div>
        <div className="row">
          <input style={{ flex: 1 }} placeholder={t('tools.batchPh')}
            value={batchCmd} onChange={e => setBatchCmd(e.target.value)} />
          <button className="btn" onClick={runBatch}>{t('common.run')}</button>
          {hint && <span className="hint">{hint}</span>}
        </div>
        {batchOut && <pre className="tools-out">{batchOut}</pre>}
      </section>

      <section>
        <h3>{t('tools.apk', { serial: serialSuffix })}</h3>
        <input type="file" accept=".apk" style={{ display: 'none' }} id="apk-input"
          onChange={e => { const f = e.target.files?.[0]; if (f) installAPK(f); e.target.value = '' }} />
        <button className="btn" disabled={!selected || busy} onClick={() => document.getElementById('apk-input')?.click()}>
          {t('tools.pickApk')}
        </button>
      </section>

      <section>
        <h3>{t('tools.shotClip', { serial: serialSuffix })}</h3>
        <div className="row">
          <button className="btn" disabled={!selected || busy} onClick={takeShot}>{t('tools.shotPreview')}</button>
          <button className="btn ghost" disabled={!selected} onClick={clipGet}>{t('tools.clipRead')}</button>
          <button className="btn ghost" disabled={!selected} onClick={clipSet}>{t('tools.clipWrite')}</button>
          {hint && <span className="hint">{hint}</span>}
        </div>
      </section>

      {shot && <PreviewModal url={shot.url} title={t('tools.shotPreview')} filename={shot.filename} onClose={closeShot} />}
    </div>
  )
}

export type { DeviceInfo }
