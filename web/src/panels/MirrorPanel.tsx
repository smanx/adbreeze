import { useEffect, useRef, useState } from 'react'
import { api, installApk, uploadFileProgress } from '../api'
import { useStore } from '../store'
import { Icon, IconButton } from '../components/Icon'
import { t, useLang, useT } from '../i18n'
import {
  androidKeyFor, applySettings, clipboardGet, clipboardSet, codecsBlockedByInsecureContext, controllerSerial,
  hasCodecsSupport, hasRTCSupport, hasSoftDecodeSupport, initController, loadMirrorSettings, rotateView, sendKey, sendOp, sendScroll, sendText,
  sendTouch, setPageMuted, startMirror, stopMirror, stopMirrorViewerAware, viewRotation, type LatencyInfo, type MirrorSettings,
} from '../mirror/controller'

const isAppleTouch = /iP(hone|ad|od)/i.test(navigator.userAgent)
  || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 0)

/** 拖拽传输任务：APK = 安装，其他文件 = 上传到 /sdcard/Download */
interface DropTask {
  id: number
  name: string
  size: number
  loaded: number
  status: 'pending' | 'active' | 'done' | 'error'
  kind: 'install' | 'upload'
  error?: string
}

/** 生效方案 → 展示名。与设置面板下拉的选项文案保持一致，避免两处说法打架 */
function planLabel(kind: string): string {
  return kind === 'webcodecs' ? t('mirror.planCodecs') : kind === 'wasm' ? t('mirror.planWasm') : kind === 'webrtc' ? t('mirror.planWebrtc') : kind
}

export default function MirrorPanel() {
  const selected = useStore(s => s.selected)
  const [running, setRunning] = useState(false)
  const [title, setTitle] = useState(t('mirror.title'))
  const [stats, setStats] = useState('')
  const [msg, setMsg] = useState('')
  const [muted, setMuted] = useState(false)
  const [phase, setPhase] = useState<'loading' | 'ready' | 'failed'>('loading')
  const [decoderKind, setDecoderKind] = useState('')
  const [renderKind, setRenderKind] = useState('')
  const [latency, setLatency] = useState<LatencyInfo | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settings, setSettings] = useState<MirrorSettings>(loadMirrorSettings(selected))
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const audioRef = useRef<HTMLAudioElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const activePointers = useRef(new Set<number>())
  const [fsOn, setFsOn] = useState(false)
  const [maxed, setMaxed] = useState(false)
  // 拖拽安装 APK / 上传其他文件：dragOver = 悬停高亮；drops = 传输队列（进度条）；
  // dropSummary = 全部传输完的汇总提示（上传到哪了 / 安装成败）
  const [dragOver, setDragOver] = useState(false)
  const [drops, setDrops] = useState<DropTask[]>([])
  const [dropSummary, setDropSummary] = useState('')
  const dropBusyRef = useRef(false)
  const dropSeq = useRef(1)
  const dropSummaryTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const [fsSupported] = useState(() =>
    typeof HTMLElement !== 'undefined' && typeof HTMLElement.prototype.requestFullscreen === 'function')
  const tr = useT()
  const lang = useLang()

  // 语言切换时同步标题（未运行时显示面板默认名；运行中由 controller 推送的标题接管）
  useEffect(() => { if (!running) setTitle(tr('mirror.title')) }, [lang]) // eslint-disable-line react-hooks/exhaustive-deps

  // 全屏状态跟随（ESC / 浏览器 UI 退出时同步按钮文案）
  useEffect(() => {
    const onChange = () => setFsOn(!!document.fullscreenElement)
    document.addEventListener('fullscreenchange', onChange)
    return () => document.removeEventListener('fullscreenchange', onChange)
  }, [])

  // 最大化：只隐藏应用自身的外壳（顶栏/设备栏/工具栏），画面占满浏览器窗口。
  // 与「全屏显示」的区别：最大化仍在浏览器窗口内（浏览器 UI 还在），
  // 全屏走原生 Fullscreen API（连浏览器 UI 一起让出）；两者可叠加。
  useEffect(() => {
    document.body.classList.toggle('mirror-max', maxed)
    return () => document.body.classList.remove('mirror-max')
  }, [maxed])

  // 最大化时工具栏已收起，补一个 Esc 退出（全屏下 Esc 归浏览器，此处不抢）
  useEffect(() => {
    if (!maxed) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !document.fullscreenElement) setMaxed(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [maxed])

  // 全屏显示：让舞台（画布+遮罩）进入浏览器原生全屏；请求被拒时兜底提示
  const toggleFullscreen = async () => {
    const el = stageRef.current
    if (!el) return
    try {
      if (document.fullscreenElement) await document.exitFullscreen()
      else await el.requestFullscreen({ navigationUI: 'hide' })
    } catch { setMsg(tr('mirror.notSupported')) }
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
        if (s.decoderKind !== undefined) setDecoderKind(s.decoderKind)
        if (s.renderKind !== undefined) setRenderKind(s.renderKind)
        if (s.latency !== undefined) setLatency(s.latency)
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

  // 设备操作按钮（不含旋转：旋转只转网页画面，见 rotateView）
  const ops = ['back', 'home', 'menu', 'notify', 'power'] as const
  const opLabel: Record<string, string> = {
    back: tr('mirror.op.back'), home: tr('mirror.op.home'), menu: tr('mirror.op.menu'),
    notify: tr('mirror.op.notify'), power: tr('mirror.op.power'),
  }

  // 视图旋转角：0 → 90 → 180 → 270 → 0 循环（角度存在 controller，这里只做展示）
  const [viewDeg, setViewDeg] = useState(viewRotation())
  const rotateViewClick = () => setViewDeg(rotateView())
  // 切标签页会让本组件重挂载、state 归零而角度仍在 controller 里，故同步一次
  useEffect(() => { setViewDeg(viewRotation()) }, [running])

  const clipboardGetClick = async () => {
    if (!selected) return
    try {
      const text = await clipboardGet(selected)
      if (!text) { setMsg(tr('mirror.clipEmpty')); return }
      try { await navigator.clipboard.writeText(text); setMsg(tr('mirror.clipCopied') + text) } catch { setMsg(tr('mirror.clipDevice') + text) }
    } catch (e) { setMsg(tr('mirror.clipFailed') + (e as Error).message) }
  }
  const clipboardPasteClick = async () => {
    if (!selected) return
    try {
      let text = ''
      try { text = await navigator.clipboard.readText() } catch { /* 无权限 */ }
      if (!text) text = prompt(tr('mirror.clipPrompt')) || ''
      if (!text) return
      await clipboardSet(selected, text, true)
      setMsg(tr('mirror.clipWritten'))
    } catch (e) { setMsg(tr('mirror.clipFailed') + (e as Error).message) }
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

  // ---------- 拖拽安装 APK / 上传其他文件 ----------
  // APK → /install 接口（push 到 /data/local/tmp 后 pm install -r）；
  // 其他文件 → 流式上传到 /sdcard/Download。文件串行处理（adb sync 单通道，
  // 并行只会争抢）。dragleave 在移入子元素时也会触发，用 contains(relatedTarget)
  // 过滤，避免高亮闪烁。
  const onStageDragOver = (e: React.DragEvent) => {
    if (![...e.dataTransfer.types].includes('Files')) return // 拖的是文本/元素，不拦
    e.preventDefault()
    setDragOver(true)
  }
  const onStageDragLeave = (e: React.DragEvent) => {
    if (e.currentTarget.contains(e.relatedTarget as Node)) return
    setDragOver(false)
  }
  const onStageDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    const files = [...e.dataTransfer.files]
    if (!files.length) return
    void runDrops(files)
  }

  const isApkFile = (f: File) => /\.apk$/i.test(f.name)
  const fmtDropSize = (n: number) => {
    const units = ['B', 'KB', 'MB', 'GB']
    let i = 0, v = n
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
    return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`
  }
  const setDrop = (id: number, patch: Partial<DropTask>) =>
    setDrops(q => q.map(x => x.id === id ? { ...x, ...patch } : x))

  const runDrops = async (files: File[]) => {
    if (!selected || dropBusyRef.current) return
    const serial = selected
    dropBusyRef.current = true
    const tasks: DropTask[] = files.map(f => ({
      id: dropSeq.current++, name: f.name, size: f.size, loaded: 0,
      status: 'pending', kind: isApkFile(f) ? 'install' : 'upload',
    }))
    setDrops(tasks)
    setDropSummary('') // 新一轮拖入时撤掉上一轮的汇总提示
    // 上传目标目录兜底创建（已存在时报错忽略）
    await api('POST', `/api/devices/${encodeURIComponent(serial)}/files/mkdir?path=${encodeURIComponent('/sdcard/Download')}`).catch(() => {})
    // 每个文件的最终结果记录在本地数组（state 里的 tasks 会被 setDrop 换成新对象，
    // 本地数组读不到最新 status，汇总必须靠这里）
    const outcomes: ('ok' | 'fail')[] = []
    for (let i = 0; i < files.length; i++) {
      const f = files[i]
      setDrop(tasks[i].id, { status: 'active' })
      try {
        if (tasks[i].kind === 'install') {
          await installApk(serial, f, loaded => setDrop(tasks[i].id, { loaded }))
        } else {
          await uploadFileProgress(serial, f, '/sdcard/Download/' + f.name, loaded => setDrop(tasks[i].id, { loaded }))
        }
        setDrop(tasks[i].id, { status: 'done', loaded: f.size })
        outcomes[i] = 'ok'
      } catch (e) {
        setDrop(tasks[i].id, { status: 'error', error: (e as Error).message })
        outcomes[i] = 'fail'
      }
    }
    dropBusyRef.current = false
    // 成功的行稍后自动消失，失败行保留供查看
    setTimeout(() => setDrops(q => q.filter(x => x.status !== 'done')), 6000)

    // 汇总提示：上传完成要说明文件去了哪（Download 目录），安装要说明成败
    const okIn: string[] = [], badIn: string[] = [], okUp: string[] = [], badUp: string[] = []
    outcomes.forEach((res, i) => {
      const bucket = tasks[i].kind === 'install' ? (res === 'ok' ? okIn : badIn) : (res === 'ok' ? okUp : badUp)
      bucket.push(files[i].name)
    })
    const nameSep = tr('mirror.dropNameSep'), sep = tr('mirror.dropSummarySep')
    const parts: string[] = []
    if (okIn.length) parts.push(tr('mirror.dropInstallOk', { names: okIn.join(nameSep) }))
    if (badIn.length) parts.push(tr('mirror.dropInstallFail', { names: badIn.join(nameSep) }))
    if (okUp.length) parts.push(tr('mirror.dropUploadDone', { names: okUp.join(nameSep) }))
    if (badUp.length) parts.push(tr('mirror.dropUploadFail', { names: badUp.join(nameSep) }))
    if (!parts.length) return
    clearTimeout(dropSummaryTimer.current) // 上一次汇总的定时清除还挂着时，先撤掉再发新的
    setDropSummary(parts.join(sep))
    dropSummaryTimer.current = setTimeout(() => setDropSummary(''), 12000)
  }

  return (
    <div className="mirror-panel">
      <div className="mirror-head">
        <span className="m-title">{title}</span>
        {/* 统计行悬停展开链路延迟分段列表（不可测的段不列入，见 controller.LatencyInfo） */}
        <span className={'m-stats' + (latency ? ' has-tip' : '')}>
          {stats}
          {latency && (
            <span className="tip-list">
              <span className="tip-title">{latency.title}</span>
              {latency.rows.map(r => (
                <span className="tip-row" key={r.label}>
                  <span className="tip-label">{r.label}</span>
                  <span className="tip-value">{r.value}</span>
                </span>
              ))}
              {latency.total && (
                <span className="tip-row tip-total">
                  <span className="tip-label">{tr('mirror.latencyTotal')}</span>
                  <span className="tip-value">{latency.total}</span>
                </span>
              )}
              {latency.note && <span className="tip-note">{latency.note}</span>}
            </span>
          )}
        </span>
        {running && decoderKind && (
          <span className="m-badge" title={tr('mirror.planTip', { plan: planLabel(decoderKind), dec: decoderKind, ren: renderKind })}>
            {/* 显示实际生效的方案名（与设置面板下拉一致）：硬解/软解是手段，
                不是用户选的方案，写出来反而与下拉对不上 */}
            {planLabel(decoderKind)}
            {/* 渲染后端三方案共用同一条管线，正常都是 WebGL——写出来只是噪音，
                只有退回 2D（无 WebGL2 的浏览器）时才值得提示 */}
            {renderKind === 'canvas2d' ? tr('mirror.render2d') : ''}
          </span>
        )}
        <span className="spacer" />
        {ops.map(op => (
          <IconButton key={op} tip={opLabel[op]} className="btn tiny ghost" disabled={!running} onClick={() => sendOp(op)}>
            <Icon name={op} />
          </IconButton>
        ))}
        <IconButton tip={tr('mirror.rotateTip', { deg: viewDeg })} className="btn tiny ghost" disabled={!running} onClick={rotateViewClick}>
          <Icon name="rotate" />
        </IconButton>
        <IconButton tip={tr('mirror.clipGetTip')} className="btn tiny ghost" disabled={!running} onClick={clipboardGetClick}>
          <Icon name="copy" />
        </IconButton>
        <IconButton tip={tr('mirror.clipPasteTip')} className="btn tiny ghost" disabled={!running} onClick={clipboardPasteClick}>
          <Icon name="clipboard" />
        </IconButton>
        <IconButton tip={muted ? tr('mirror.unmute') : tr('mirror.mute')} className="btn tiny" onClick={muteClick}>
          <Icon name={muted ? 'speaker-off' : 'speaker'} />
        </IconButton>
        <IconButton
          tip={!fsSupported ? tr('mirror.fsUnsupported') : fsOn ? tr('mirror.fsExit') : tr('mirror.fsEnter')}
          className="btn tiny ghost" disabled={!fsSupported} onClick={toggleFullscreen}>
          <Icon name={fsOn ? 'minimize' : 'maximize'} />
        </IconButton>
        <IconButton
          tip={maxed ? tr('mirror.maxExit') : tr('mirror.maxEnter')}
          className="btn tiny ghost" onClick={() => setMaxed(v => !v)}>
          <Icon name={maxed ? 'collapse' : 'expand'} />
        </IconButton>
        <IconButton tip={tr('common.settings')} className="btn tiny ghost" onClick={() => setSettingsOpen(v => !v)}>
          <Icon name="settings" />
        </IconButton>
        {running
          ? <IconButton tip={tr('mirror.stop')} className="btn tiny danger" onClick={stopClick}><Icon name="stop" /></IconButton>
          : <IconButton tip={tr('mirror.start')} className="btn tiny" disabled={!selected} onClick={startClick}><Icon name="play" /></IconButton>}
      </div>

      {settingsOpen && (
        <div className="mirror-settings">
          <label>{tr('mirror.scheme')}
            <select value={settings.mode} onChange={e => set('mode', e.target.value)}>
              {/* 选项只留方案名：括号里的描述会和实际语义打架（「自动」不是一定硬解、
                  「WASM 软解」也兜不住非 Baseline 码流），不可用的原因挪到 title */}
              <option value="auto">{tr('mirror.optAuto')}</option>
              <option value="codecs" disabled={!hasCodecsSupport()}
                title={hasCodecsSupport() ? undefined
                  : codecsBlockedByInsecureContext() ? tr('mirror.needHttps') : tr('mirror.noCodecs')}>
                {tr('mirror.planCodecs')}
              </option>
              <option value="webrtc" disabled={!hasRTCSupport()}
                title={hasRTCSupport() ? undefined : tr('mirror.noWebrtc')}>
                {tr('mirror.planWebrtc')}
              </option>
              <option value="wasm" disabled={!hasSoftDecodeSupport()}
                title={hasSoftDecodeSupport() ? undefined : tr('mirror.noWasm')}>
                {tr('mirror.planWasm')}
              </option>
            </select>
          </label>
          <label>{tr('mirror.audio')}
            <select value={settings.audioMode} onChange={e => set('audioMode', e.target.value)}>
              <option value="both">{tr('mirror.audioBoth')}</option>
              <option value="mute">{tr('mirror.audioMute')}</option>
              <option value="device">{tr('mirror.audioDevice')}</option>
            </select>
          </label>
          <label>{tr('mirror.bitrate')}
            <select value={settings.bitrateMbps} onChange={e => set('bitrateMbps', e.target.value)}>
              {[2, 4, 8, 16, 20].map(v => <option key={v} value={v}>{v} Mbps</option>)}
            </select>
          </label>
          <label>{tr('mirror.shortSize')}
            <select value={settings.shortSize} onChange={e => set('shortSize', e.target.value)}>
              {[1440, 1280, 1080, 900, 720, 480].map(v => <option key={v} value={v}>{v}{v === 720 ? tr('common.defaultParens') : ''}</option>)}
            </select>
          </label>
          <label>{tr('mirror.fps')}
            <select value={settings.maxFps} onChange={e => set('maxFps', e.target.value)}>
              {[15, 30, 60, 120].map(v => <option key={v} value={v}>{v} fps</option>)}
            </select>
          </label>
          <button className="btn" onClick={applyClick}>{tr('common.apply')}</button>
          <span className="hint">{tr('mirror.restartHint')}</span>
        </div>
      )}

      <div className="mirror-body">
        <div className="mirror-stage" ref={stageRef}
          onDragOver={onStageDragOver} onDragLeave={onStageDragLeave} onDrop={onStageDrop}>
          <canvas id="mirror-canvas" ref={canvasRef} width={720} height={1280} tabIndex={0} />
          {/* 拖拽悬停高亮：pointer-events 关掉，事件继续落到 stage，dragleave 才不闪 */}
          {dragOver && (
            <div className="mirror-overlay drop-zone">
              <span className="ov-icon">📦</span>
              <span className="ov-text">{tr('mirror.dropInstallTip')}</span>
            </div>
          )}
          {/* 首帧前盖住画布（防旧帧错觉），失败时显示错误 */}
          {running && phase !== 'ready' && (
            <div className={'mirror-overlay' + (phase === 'failed' ? ' failed' : '')}>
              {phase === 'loading' ? (
                <>
                  <span className="spinner" />
                  <span className="ov-text">{msg || tr('mirror.acquiring')}</span>
                </>
              ) : (
                <>
                  <span className="ov-icon">⚠️</span>
                  <span className="ov-text">{msg || tr('mirror.failed')}</span>
                  <button className="btn tiny" onClick={startClick}>{tr('common.retry')}</button>
                </>
              )}
            </div>
          )}
        </div>
        <audio ref={audioRef} autoPlay style={{ display: 'none' }} />
      </div>
      {/* 最大化时工具栏整体收起，留这个悬浮按钮退出（在 .mirror-head 之外，不会被一起藏） */}
      {maxed && (
        <IconButton tip={tr('mirror.maxExit')} className="btn tiny ghost max-exit" onClick={() => setMaxed(false)}>
          <Icon name="collapse" />
        </IconButton>
      )}
      {msg && <div className="mirror-msg">{msg}</div>}
      {dropSummary && <div className="mirror-msg drop-summary">{dropSummary}</div>}
      {/* 拖拽传输队列：APK 安装 / 文件上传进度（复用文件面板的传输条样式） */}
      {drops.length > 0 && (
        <div className="files-xfer mirror-drops">
          {drops.map(x => (
            <div key={x.id} className={'files-xfer-row' + (x.status === 'error' ? ' err' : '')}>
              <span className="fx-name" title={x.name}>{x.kind === 'install' ? '📦 ' : '📄 '}{x.name}</span>
              <div className="fx-bar">
                <div className="fx-fill" style={{
                  width: `${x.size ? Math.min(100, x.loaded / x.size * 100) : (x.status === 'done' ? 100 : 0)}%`,
                  background: x.status === 'error' ? 'var(--danger)' : x.status === 'done' ? 'var(--primary)' : undefined,
                }} />
              </div>
              <span className="fx-info">
                {x.status === 'error' ? x.error
                  : x.status === 'done' ? tr('mirror.dropDone')
                  : x.status === 'active' && x.size ? `${fmtDropSize(x.loaded)} / ${fmtDropSize(x.size)}`
                  : '…'}
              </span>
            </div>
          ))}
        </div>
      )}
      {!selected && <div className="hint" style={{ padding: '6px 2px' }}>{tr('panel.selectDevice.')}</div>}
    </div>
  )
}
