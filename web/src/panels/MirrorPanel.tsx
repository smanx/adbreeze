import { useEffect, useRef, useState } from 'react'
import { useStore } from '../store'
import {
  androidKeyFor, applySettings, clipboardGet, clipboardSet, controllerSerial, hasCodecsSupport,
  initController, loadMirrorSettings, sendKey, sendOp, sendScroll, sendText, sendTouch,
  setPageMuted, startMirror, stopMirror, stopMirrorViewerAware, type MirrorSettings,
} from '../mirror/controller'

const isAppleTouch = /iP(hone|ad|od)/i.test(navigator.userAgent)
  || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)

export default function MirrorPanel() {
  const selected = useStore(s => s.selected)
  const [running, setRunning] = useState(false)
  const [title, setTitle] = useState('投屏')
  const [stats, setStats] = useState('')
  const [msg, setMsg] = useState('')
  const [muted, setMuted] = useState(false)
  const [phase, setPhase] = useState<'loading' | 'ready' | 'failed'>('loading')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settings, setSettings] = useState<MirrorSettings>(loadMirrorSettings(selected))
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const audioRef = useRef<HTMLAudioElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const activePointers = useRef(new Set<number>())
  const [fsOn, setFsOn] = useState(false)
  const [fsSupported] = useState(() =>
    typeof HTMLElement !== 'undefined' && typeof HTMLElement.prototype.requestFullscreen === 'function')

  // 全屏状态跟随（ESC / 浏览器 UI 退出时同步按钮文案）
  useEffect(() => {
    const onChange = () => setFsOn(!!document.fullscreenElement)
    document.addEventListener('fullscreenchange', onChange)
    return () => document.removeEventListener('fullscreenchange', onChange)
  }, [])

  // 全屏显示：让舞台（画布+遮罩）进入浏览器原生全屏；请求被拒时兜底提示
  const toggleFullscreen = async () => {
    const el = stageRef.current
    if (!el) return
    try {
      if (document.fullscreenElement) await document.exitFullscreen()
      else await el.requestFullscreen({ navigationUI: 'hide' })
    } catch { setMsg('不支持') }
  }

  // 控制器初始化 + 状态回调（一次；设备切换由 start/stop 处理）
  useEffect(() => {
    initController(canvasRef.current!, audioRef.current!, {
      onStatus: s => {
        if (s.title !== undefined) setTitle(s.title)
        if (s.stats !== undefined) setStats(s.stats)
        if (s.msg !== undefined) setMsg(s.msg)
        if (s.running !== undefined) setRunning(s.running)
        if (s.muted !== undefined) setMuted(s.muted)
        if (s.phase !== undefined) setPhase(s.phase)
      },
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 切换选中设备：停掉当前观看
  useEffect(() => {
    if (controllerSerial() && controllerSerial() !== selected) {
      stopMirror(false)
      setRunning(false)
    }
    setSettings(loadMirrorSettings(selected))
  }, [selected])

  // ---------- 输入处理（指针 / iOS 触摸 / 键盘 / 滚轮） ----------
  useEffect(() => {
    const canvas = canvasRef.current!
    const onPointerDown = (e: PointerEvent) => {
      e.preventDefault()
      // 鼠标按键映射：左键=触控/确定，右键=返回，中键=Home（不产生触控轨迹）
      if (e.pointerType === 'mouse' && (e.button === 1 || e.button === 2)) {
        sendOp(e.button === 1 ? 'home' : 'back')
        return
      }
      activePointers.current.add(e.pointerId)
      try { canvas.setPointerCapture(e.pointerId) } catch { /* iOS 可能抛 */ }
      sendTouch('down', e.clientX, e.clientY, e.pointerId)
    }
    const onPointerMove = (e: PointerEvent) => {
      if (!activePointers.current.has(e.pointerId)) return
      sendTouch('move', e.clientX, e.clientY, e.pointerId)
    }
    const onPointerEnd = (e: PointerEvent) => {
      if (!activePointers.current.delete(e.pointerId)) return
      sendTouch('up', e.clientX, e.clientY, e.pointerId)
    }
    const onTouchStart = (e: TouchEvent) => {
      e.preventDefault()
      for (const t of e.changedTouches) {
        activePointers.current.add(t.identifier)
        sendTouch('down', t.clientX, t.clientY, t.identifier)
      }
    }
    const onTouchMove = (e: TouchEvent) => {
      e.preventDefault()
      for (const t of e.changedTouches) {
        if (activePointers.current.has(t.identifier)) sendTouch('move', t.clientX, t.clientY, t.identifier)
      }
    }
    const onTouchEnd = (e: TouchEvent) => {
      for (const t of e.changedTouches) {
        if (activePointers.current.delete(t.identifier)) sendTouch('up', t.clientX, t.clientY, t.identifier)
      }
    }
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      sendScroll(e.clientX, e.clientY, Math.sign(e.deltaX), Math.sign(e.deltaY))
    }
    // 键盘：功能键走 keycode 注入；可见单字符（含标点）与中文走 INJECT_TEXT 文本注入，
    // 中文经 IME 在 compositionend 提交（输入过程中不发，避免拼音字母漏进设备）。
    let composing = false
    const onCompositionStart = () => { composing = true }
    const onCompositionEnd = (e: CompositionEvent) => {
      composing = false
      if (e.data) { e.preventDefault(); sendText(e.data) }
    }
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault()
      if (e.ctrlKey || e.metaKey || e.altKey) return // 保留浏览器快捷键
      if (composing) return // 输入法候选过程不注入
      const code = androidKeyFor(e.key)
      if (code !== null) {
        sendKey(e.key, { shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, altKey: e.altKey }, true)
        sendKey(e.key, { shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, altKey: e.altKey }, false)
      } else if (e.key.length === 1) {
        sendText(e.key)
      }
    }
    const onContext = (e: Event) => e.preventDefault()

    // iOS/iPadOS 走原生 touch 事件（Safari 的 pointer 事件对触摸不稳定）
    if (isAppleTouch || !window.PointerEvent) {
      canvas.addEventListener('touchstart', onTouchStart, { passive: false })
      canvas.addEventListener('touchmove', onTouchMove, { passive: false })
      canvas.addEventListener('touchend', onTouchEnd)
      canvas.addEventListener('touchcancel', onTouchEnd)
    } else {
      canvas.addEventListener('pointerdown', onPointerDown)
      canvas.addEventListener('pointermove', onPointerMove)
      canvas.addEventListener('pointerup', onPointerEnd)
      canvas.addEventListener('pointercancel', onPointerEnd)
    }
    canvas.addEventListener('wheel', onWheel, { passive: false })
    canvas.addEventListener('keydown', onKey)
    canvas.addEventListener('compositionstart', onCompositionStart)
    canvas.addEventListener('compositionend', onCompositionEnd)
    canvas.addEventListener('contextmenu', onContext)
    return () => {
      canvas.removeEventListener('touchstart', onTouchStart)
      canvas.removeEventListener('touchmove', onTouchMove)
      canvas.removeEventListener('touchend', onTouchEnd)
      canvas.removeEventListener('touchcancel', onTouchEnd)
      canvas.removeEventListener('pointerdown', onPointerDown)
      canvas.removeEventListener('pointermove', onPointerMove)
      canvas.removeEventListener('pointerup', onPointerEnd)
      canvas.removeEventListener('pointercancel', onPointerEnd)
      canvas.removeEventListener('wheel', onWheel)
      canvas.removeEventListener('keydown', onKey)
      canvas.removeEventListener('compositionstart', onCompositionStart)
      canvas.removeEventListener('compositionend', onCompositionEnd)
      canvas.removeEventListener('contextmenu', onContext)
    }
  }, [])

  // 画布点击后保持键盘焦点（键盘输入的前提）；失焦时若仍在本面板则拉回。
  useEffect(() => {
    const canvas = canvasRef.current!
    const refocus = () => {
      if (!canvas.contains(document.activeElement)) canvas.focus({ preventScroll: true })
    }
    canvas.addEventListener('pointerup', refocus)
    return () => canvas.removeEventListener('pointerup', refocus)
  }, [])

  const ops = ['back', 'home', 'menu', 'notify', 'power', 'rotate'] as const
  const opLabel: Record<string, string> = {
    back: '返回', home: '主页', menu: '菜单', notify: '通知', power: '电源', rotate: '旋转',
  }

  const clipboardGetClick = async () => {
    if (!selected) return
    try {
      const text = await clipboardGet(selected)
      if (!text) { setMsg('剪贴板为空'); return }
      try { await navigator.clipboard.writeText(text); setMsg('已复制到本机: ' + text) } catch { setMsg('设备剪贴板: ' + text) }
    } catch (e) { setMsg('剪贴板操作失败: ' + (e as Error).message) }
  }
  const clipboardPasteClick = async () => {
    if (!selected) return
    try {
      let text = ''
      try { text = await navigator.clipboard.readText() } catch { /* 无权限 */ }
      if (!text) text = prompt('输入要写入设备剪贴板的文本：') || ''
      if (!text) return
      await clipboardSet(selected, text, true)
      setMsg('已写入设备剪贴板并粘贴')
    } catch (e) { setMsg('剪贴板操作失败: ' + (e as Error).message) }
  }

  const stopClick = async () => {
    if (controllerSerial()) await stopMirrorViewerAware()
    setRunning(false)
  }
  const startClick = () => { if (selected) startMirror(selected) }
  const muteClick = () => { setPageMuted(!muted); setMuted(!muted) }
  const applyClick = () => {
    if (selected) applySettings(selected, settings)
    setSettingsOpen(false)
  }

  const set = (k: keyof MirrorSettings, v: string) =>
    setSettings(s => ({ ...s, [k]: k === 'mode' || k === 'audioMode' ? v : +v }))

  return (
    <div className="mirror-panel">
      <div className="mirror-head">
        <span className="m-title">{title}</span>
        <span className="m-stats">{stats}</span>
        <span className="spacer" />
        {ops.map(op => (
          <button key={op} className="btn tiny ghost" disabled={!running} onClick={() => sendOp(op)}>{opLabel[op]}</button>
        ))}
        <button className="btn tiny ghost" disabled={!running} onClick={clipboardGetClick}>取剪贴板</button>
        <button className="btn tiny ghost" disabled={!running} onClick={clipboardPasteClick}>粘贴到设备</button>
        <button className="btn tiny" onClick={muteClick}>{muted ? '🔇 已静音' : '🔊 声音'}</button>
        <button className="btn tiny ghost" onClick={toggleFullscreen} disabled={!fsSupported}>
          {!fsSupported ? '⛶ 不支持' : fsOn ? '⛶ 退出全屏' : '⛶ 全屏显示'}
        </button>
        <button className="btn tiny ghost" onClick={() => setSettingsOpen(v => !v)}>⚙ 设置</button>
        {running
          ? <button className="btn tiny danger" onClick={stopClick}>停止</button>
          : <button className="btn tiny" disabled={!selected} onClick={startClick}>开始投屏</button>}
      </div>

      {settingsOpen && (
        <div className="mirror-settings">
          <label>投屏方案
            <select value={settings.mode} onChange={e => set('mode', e.target.value)}>
              <option value="codecs" disabled={!hasCodecsSupport()}>
                WebCodecs（WS 直推）{hasCodecsSupport() ? '' : '（浏览器不支持）'}
              </option>
              <option value="webrtc">WebRTC（原生低延迟）</option>
            </select>
          </label>
          <label>手机声音
            <select value={settings.audioMode} onChange={e => set('audioMode', e.target.value)}>
              <option value="both">🔊🔊 两边都出声（默认）</option>
              <option value="mute">🔇 静音（采集到网页）</option>
              <option value="device">🔊 出声（设备扬声器）</option>
            </select>
          </label>
          <label>码率
            <select value={settings.bitrateMbps} onChange={e => set('bitrateMbps', e.target.value)}>
              {[2, 4, 8, 16, 20].map(v => <option key={v} value={v}>{v} Mbps</option>)}
            </select>
          </label>
          <label>分辨率（短边）
            <select value={settings.shortSize} onChange={e => set('shortSize', e.target.value)}>
              {[1440, 1280, 1080, 900, 720, 480].map(v => <option key={v} value={v}>{v}{v === 720 ? '（默认）' : ''}</option>)}
            </select>
          </label>
          <label>帧率
            <select value={settings.maxFps} onChange={e => set('maxFps', e.target.value)}>
              {[15, 30, 60, 120].map(v => <option key={v} value={v}>{v} fps</option>)}
            </select>
          </label>
          <button className="btn" onClick={applyClick}>应用</button>
          <span className="hint">改编码器参数需重启流，约 1 秒</span>
        </div>
      )}

      <div className="mirror-body">
        <div className="mirror-stage" ref={stageRef}>
          <canvas id="mirror-canvas" ref={canvasRef} width={720} height={1280} tabIndex={0} />
          {/* 首帧前盖住画布（防旧帧错觉），失败时显示错误 */}
          {running && phase !== 'ready' && (
            <div className={'mirror-overlay' + (phase === 'failed' ? ' failed' : '')}>
              {phase === 'loading' ? (
                <>
                  <span className="spinner" />
                  <span className="ov-text">{msg || '正在获取画面…'}</span>
                </>
              ) : (
                <>
                  <span className="ov-icon">⚠️</span>
                  <span className="ov-text">{msg || '画面获取失败'}</span>
                  <button className="btn tiny" onClick={startClick}>重试</button>
                </>
              )}
            </div>
          )}
        </div>
        <audio ref={audioRef} autoPlay style={{ display: 'none' }} />
      </div>
      {msg && <div className="mirror-msg">{msg}</div>}
      {!selected && <div className="hint" style={{ padding: '6px 2px' }}>请先在设备列表中选择设备。</div>}
    </div>
  )
}
