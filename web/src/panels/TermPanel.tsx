import { useEffect, useRef, useState } from 'react'
import type { Terminal } from '@xterm/xterm'
import { ensureSession, wsUrl } from '../api'
import { useStore } from '../store'
import { t, useT } from '../i18n'

// 交互式终端：/ws/term/{serial} PTY（上行 JSON input，下行二进制 ANSI 输出）
export default function TermPanel() {
  const selected = useStore(s => s.selected)
  const boxRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const [state, setState] = useState<'closed' | 'opening' | 'open'>('closed')
  const tr = useT()

  // 动态加载 xterm（避免首屏体积）
  const ensureXterm = async () => {
    if (termRef.current) return termRef.current
    const { Terminal } = await import('@xterm/xterm')
    const { FitAddon } = await import('@xterm/addon-fit')
    const term = new Terminal({ convertEol: false, fontSize: 13, cursorBlink: true, theme: { background: '#10141a' } })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(boxRef.current!)
    fit.fit()
    termRef.current = term
    return term
  }

  const openTerm = async () => {
    if (!selected || state === 'open') return
    setState('opening')
    try {
      const term = await ensureXterm()
      term.clear()
      await ensureSession() // WS 凭会话 cookie / token 鉴权，握手前确保有效
      const ws = new WebSocket(
        wsUrl(`/ws/term/${encodeURIComponent(selected)}`),
      )
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws
      const dataHandler = term.onData(d => {
        if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'input', data: d }))
      })
      ws.onopen = () => { setState('open'); term.focus() }
      ws.onmessage = ev => {
        if (ev.data instanceof ArrayBuffer) term.write(new Uint8Array(ev.data))
      }
      ws.onclose = ev => {
        dataHandler.dispose()
        setState('closed')
        wsRef.current = null
        term.write(`\r\n\x1b[33m[${t('term.closed', { reason: ev.reason ? t('term.reasonSuffix', { reason: ev.reason }) : '' })}]\x1b[0m\r\n`)
      }
    } catch (e) {
      setState('closed')
      termRef.current?.write(`\r\n\x1b[31m[${t('term.openFailed', { msg: (e as Error).message })}]\x1b[0m\r\n`)
    }
  }

  const closeTerm = () => {
    const ws = wsRef.current
    if (ws && ws.readyState === 1) ws.send(JSON.stringify({ type: 'exit' }))
    ws?.close()
  }

  useEffect(() => () => { // 卸载/切换设备时关闭终端
    const ws = wsRef.current
    if (ws && ws.readyState === 1) ws.send(JSON.stringify({ type: 'exit' }))
    ws?.close()
    wsRef.current = null
  }, [selected])

  return (
    <div className="panel term-panel">
      <div className="term-bar">
        <button className="btn" disabled={!selected || state === 'open'} onClick={openTerm}>{tr('term.open')}</button>
        <button className="btn ghost" disabled={state !== 'open'} onClick={closeTerm}>{tr('common.close')}</button>
        <span className="hint">
          {state === 'open' ? tr('term.connected') : state === 'opening' ? tr('common.loading') : selected ? tr('term.deviceOf', { s: selected }) : tr('panel.selectDeviceShort')}
        </span>
      </div>
      <div className="term-box" ref={boxRef} />
    </div>
  )
}
