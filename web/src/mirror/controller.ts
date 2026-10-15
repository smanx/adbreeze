// 投屏控制器：从 legacy 页完整迁移的逻辑（单例，一个标签页一条活跃观看会话）。
// 保留所有既有修复：双方案降级、iOS touch、观众感知停止、WSOLA 音频、
// SPS 级别补丁（服务端）、固定 20ms RTP（服务端）、按设备记忆设置。
//
// 解码/渲染层移植自 ya-webadb（Tango）：
//   * WebCodecs 封装带回放缓冲、解码器回收/硬解故障恢复与统计埋点（decoder.ts）
//   * WebGL 上屏 + 着色器缩放 + rAF 帧门控 + ResizeObserver 重绘（render.ts）
//   * h264bsd WASM 软解兜底，可在 HTTP 非安全上下文使用（soft-decoder.ts）
import { api } from '../api'
import { FrameRenderer, type RenderKind } from './render'
import { H264Decoder, spsProfileIdc } from './decoder'
import { SoftH264Decoder, hasWebAssembly } from './soft-decoder'

const MIRROR_DEFAULTS = {
  mode: 'auto', audioMode: 'both', bitrateMbps: 8, shortSize: 720, maxFps: 60,
} as const
export type MirrorSettings = {
  /** 投屏方案：auto = 优先 WebCodecs，不可用时降级；codecs / webrtc / wasm 为强制指定 */
  mode: 'auto' | 'codecs' | 'webrtc' | 'wasm'
  audioMode: string
  bitrateMbps: number
  shortSize: number
  maxFps: number
}

const mirrorSettingsKey = (serial: string) => 'ADBreeze-mirror-settings-v4:' + serial

export function hasCodecsSupport() {
  // WebCodecs 仅在安全上下文（HTTPS / localhost / 127.0.0.1）暴露，HTTP+IP 访问时
  // Chrome/Edge/Android 直接不提供 VideoDecoder——这不是浏览器版本问题
  return window.isSecureContext && 'VideoDecoder' in window && 'EncodedVideoChunk' in window
}
// 非安全上下文导致 WebCodecs 不可用（浏览器本身支持，只是 HTTP+IP 访问被禁用）
export function codecsBlockedByInsecureContext() {
  return !window.isSecureContext
}
export function hasRTCSupport() {
  return 'RTCPeerConnection' in window
}
/** WASM 软解可用：不要求安全上下文，是 HTTP+IP 访问下唯一非 WebRTC 的选择 */
export function hasSoftDecodeSupport() {
  return hasWebAssembly()
}

export function loadMirrorSettings(serial: string | null): MirrorSettings {
  let saved: Partial<MirrorSettings> = {}
  if (serial) {
    try { saved = JSON.parse(localStorage.getItem(mirrorSettingsKey(serial)) || '{}') } catch { /* 忽略坏存档 */ }
  }
  return { ...MIRROR_DEFAULTS, ...saved }
}
export function saveMirrorSettings(serial: string, s: MirrorSettings) {
  try { localStorage.setItem(mirrorSettingsKey(serial), JSON.stringify(s)) } catch { /* 忽略 */ }
}
/** 画质相关字段（mode 不属于画质：仅切方案不应重启设备端编码器） */
function qualityJSON(s: MirrorSettings) {
  return JSON.stringify([s.audioMode, s.bitrateMbps, s.shortSize, s.maxFps])
}
function savedQualityJSON(serial: string | null) {
  return qualityJSON(loadMirrorSettings(serial))
}

export interface MirrorStatus {
  title: string
  stats: string
  msg: string
  running: boolean
  muted: boolean
  /** 生效的解码路径与渲染后端（徽标展示，便于确认是否走软解/降级） */
  decoderKind: 'webcodecs' | 'wasm' | 'webrtc' | ''
  renderKind: RenderKind | ''
  // 画面阶段：loading = 等待首帧（画布盖遮罩，避免旧帧造成"已在投"的错觉），
  // ready = 首帧已渲染，failed = 等待超时/启动失败（显示失败提示）
  phase: 'loading' | 'ready' | 'failed'
}

interface Events {
  onStatus: (s: Partial<MirrorStatus>) => void
}

// ---------- 模块级单例状态（与 legacy 页一致） ----------
let events: Events | null = null
let canvasEl: HTMLCanvasElement | null = null
let audioEl: HTMLAudioElement | null = null

let mirrorWS: WebSocket | null = null
let mirrorSerial: string | null = null
let deviceAudio = false
let audioDup = false
let webrtcMode = false
/** 用户强制使用 WASM 软解（mode='wasm'） */
let forcedSoftDecode = false
let webrtcPC: RTCPeerConnection | null = null
let videoW = 720
let videoH = 1280
let waitingKeyframe = false

// 解码/渲染管线（每次会话重建）
let renderer: FrameRenderer | null = null
let hwDecoder: H264Decoder | null = null
let softDecoder: SoftH264Decoder | null = null
/** 当前生效的解码路径：软解兜底时为 wasm */
let decoderKind: MirrorStatus['decoderKind'] = ''
/** 解码器需要配置包（新流或 codec 变化）：收到 config 帧后重建解码器 */
let needConfig = true
/**
 * 视图旋转角（0/90/180/270）。存在模块层而非只存渲染器里，
 * 因为渲染器会随画布被 React 换掉而重建，用户设的角度不该跟着丢。
 */
let viewDeg = 0
/** 会话代际：异步回调（软解 wasm 加载）据此判断会话是否已失效 */
let sessionSeq = 0

let audioWS: WebSocket | null = null
let audioDecoder: AudioDecoder | null = null
let audioCtx: AudioContext | null = null
let audioNode: AudioWorkletNode | null = null
let audioGain: GainNode | null = null
let audioRetryTimer: ReturnType<typeof setTimeout> | null = null
let audioPackets = 0
let audioOn = false
let pageMuted = false
let mirrorRestarting = false

let frameCount = 0
let byteCount = 0
let lastTick = 0
let configData: Uint8Array | null = null
/** 最近一次对外广播的状态（含初始空值），供组件重挂载时回放 */
let lastStatus: Partial<MirrorStatus> = {}

function setStatus(s: Partial<MirrorStatus>) {
  if (events) events.onStatus(s)
  // 缓存最近状态：React 重挂载（切标签页再回来）后组件 state 归零，
  // 而会话仍在跑，不回放就会出现「画面在动但按钮全禁用、显示开始投屏」。
  lastStatus = { ...lastStatus, ...s }
}
function showMsg(text: string) { setStatus({ msg: text }) }

let visibleBound = false

export function initController(canvas: HTMLCanvasElement, audio: HTMLAudioElement, ev: Events) {
  canvasEl = canvas
  audioEl = audio
  events = ev
  ev.onStatus(lastStatus)
  // 后台标签页：只暂停上屏（省 GPU/合成开销），解码继续，切回即时恢复画面。
  // 不暂停解码是有意为之：Hub 的「丢最旧保最新」会让暂停期间的帧丢失，
  // 恢复后要等下一个关键帧（GOP 2s）才有画面，体验倒退。
  // 只注册一次：本函数随组件重挂载反复调用，重复挂会累积监听器。
  if (!visibleBound) {
    visibleBound = true
    document.addEventListener('visibilitychange', () => {
      renderer?.setPaused(document.visibilityState === 'hidden')
    })
  }
}
export function controllerSerial() { return mirrorSerial }

// ---------- 通用发送 ----------
function wsSend(obj: unknown) {
  if (mirrorWS && mirrorWS.readyState === 1) mirrorWS.send(JSON.stringify(obj))
}
export function sendTouch(action: string, clientX: number, clientY: number, pid: number) {
  if (!canvasEl) return
  const p = videoCoords(clientX, clientY)
  wsSend({ type: 'touch', action, x: p.x, y: p.y, pid })
}
export function sendScroll(clientX: number, clientY: number, dx: number, dy: number) {
  const p = videoCoords(clientX, clientY)
  wsSend({ type: 'scroll', x: p.x, y: p.y, dx, dy })
}
export function sendKey(key: string, meta: { shiftKey: boolean; ctrlKey: boolean; altKey: boolean }, down: boolean) {
  const code = androidKeyFor(key)
  if (code === null) return
  let m = 0
  if (meta.shiftKey) m |= 1
  if (meta.ctrlKey) m |= 0x1000
  if (meta.altKey) m |= 2
  wsSend({ type: 'key', code, action: down ? 'down' : 'up', meta: m })
}
// 文本注入（scrcpy INJECT_TEXT）：可打印字符与中文等非 ASCII 输入统一走此通道，
// 服务端已有 type:'text' 处理（control.go），设备端无需输入框焦点限制。
export function sendText(text: string) {
  if (text) wsSend({ type: 'text', text })
}
export function sendOp(name: string) { wsSend({ type: 'op', name }) }

/**
 * 客户端坐标 → 视频坐标。优先交给渲染器（它掌握视图旋转与画布实际比例），
 * 渲染器尚未就绪时退回按 videoW/H 的等比映射（未旋转情形两者一致）。
 */
function videoCoords(clientX: number, clientY: number) {
  const viaRenderer = renderer?.clientToVideo(clientX, clientY)
  if (viaRenderer) return viaRenderer
  if (!canvasEl) return { x: 0, y: 0 }
  const rect = canvasEl.getBoundingClientRect()
  return {
    x: Math.max(0, Math.min(videoW - 1, Math.round((clientX - rect.left) * videoW / rect.width))),
    y: Math.max(0, Math.min(videoH - 1, Math.round((clientY - rect.top) * videoH / rect.height))),
  }
}

/** 视图旋转（仅旋转网页画面，不旋转设备）：0 → 90 → 180 → 270 循环 */
export function rotateView() {
  viewDeg = (viewDeg + 90) % 360
  renderer?.setViewRotation(viewDeg)
  return viewDeg
}
/** 当前视图旋转角 */
export function viewRotation(): number {
  return viewDeg
}

// ---------- 关键帧提示 ----------
function waitKeyframe() {
  waitingKeyframe = true
  setStatus({ phase: 'loading' })
  showMsg('正在等待画面关键帧…（编码器 GOP 约 2 秒，静止画面下个别机型可能更久）')
  armFirstFrameTimeout()
}
function clearKeyframeWait() {
  if (waitingKeyframe) { waitingKeyframe = false; showMsg('') }
}

// ---------- 首帧等待 ----------
// 首帧到达前 phase=loading（遮罩盖住旧画面）；超时 12 秒视为失败。
// 超时判定不依赖 waitingKeyframe（startMirror 路径不会设置它），用 firstFrameShown 标记：
// 只要超时时刻首帧还没画上，就必须转失败——修复 WebRTC 无帧时无限卡在 loading 的问题。
const FIRST_FRAME_TIMEOUT = 12000
let firstFrameTimer: ReturnType<typeof setTimeout> | null = null
let firstFrameShown = false

function armFirstFrameTimeout() {
  disarmFirstFrameTimeout()
  waitingKeyframe = true
  firstFrameShown = false
  firstFrameTimer = setTimeout(() => {
    firstFrameTimer = null
    if (firstFrameShown || !mirrorSerial) return
    waitingKeyframe = false
    setStatus({
      phase: 'failed',
      msg: webrtcMode
        ? '画面获取失败：WebRTC 等待首帧超时（设备编码器可能未推流或 ICE 连接失败），可重试或改用 WebCodecs 方案'
        : '画面获取失败：等待首帧超时（设备可能离线、熄屏或编码器未响应）',
    })
  }, FIRST_FRAME_TIMEOUT)
}
function disarmFirstFrameTimeout() {
  if (firstFrameTimer) { clearTimeout(firstFrameTimer); firstFrameTimer = null }
}

// 首帧成功：解除等待状态（WebCodecs 在解码输出回调、WebRTC 在 ontrack 里调用）
function firstFrameReady() {
  firstFrameShown = true
  disarmFirstFrameTimeout()
  if (waitingKeyframe) { waitingKeyframe = false; showMsg('') }
  setStatus({ phase: 'ready' })
}

// ---------- 视频帧统计 ----------
// 渲染/解码计数来自移植的管线（Tango 埋点思路）：积压深度与丢帧数直接反映
// 「卡在哪里」——decodeQueueSize 增长说明解码跟不上网络，丢帧增长说明
// 渲染跟不上解码。丢帧按「本秒增量」显示（与 fps 同窗口），累计值无意义。
// 解码方案与渲染后端不在这里出现：那是徽标的职责，两处都写会重复同一信息。
let lastDropTotal = 0
function tickStats() {
  const now = performance.now()
  if (now - lastTick >= 1000) {
    const parts: string[] = [`${frameCount} fps`, `${(byteCount * 8 / 1e6).toFixed(1)} Mbps`]
    if (audioOn) parts.push('🔊')
    if (renderer) {
      const c = renderer.countersRef
      const dropTotal = c.skipped + (hwDecoder?.counters.framesDroppedBackpressure ?? 0)
      const dropDelta = dropTotal - lastDropTotal
      lastDropTotal = dropTotal
      if (dropDelta > 0) parts.push(`丢帧 ${dropDelta}`)
      const q = hwDecoder?.decodeQueueSize ?? 0
      if (q > 3) parts.push(`积压 ${q}`)
    }
    setStatus({ stats: parts.join(' · ') })
    frameCount = 0; byteCount = 0; lastTick = now
  }
}

// ---------- 视频通道（WS 直推 + WebCodecs / WASM 软解） ----------

/**
 * 渲染器（跨会话复用；context lost 由其内部降级）。
 *
 * React 重挂载（切标签页再回来）会换掉画布节点，旧渲染器绑定的画布已脱离文档，
 * 继续复用就是「解码统计正常、屏幕一动不动」，尺寸也停在画布的属性默认值上。
 * 因此发现换脸就重建，视图旋转角由 viewDeg 承载，不随重建丢失。
 */
function ensureRenderer(): FrameRenderer | null {
  if (!canvasEl) return null
  if (renderer && (renderer.canvasElement !== canvasEl || !canvasEl.isConnected)) {
    renderer.dispose()
    renderer = null
  }
  if (!renderer) {
    renderer = new FrameRenderer(canvasEl, {
      // 同一画布的上下文类型不可更换：WebGL 彻底丢失后 2d/bitmap 也拿不到，
      // 只能刷新页面重建画布
      onUnrecoverable: () => setStatus({
        phase: 'failed',
        msg: '画面获取失败：GPU 上下文丢失且无法恢复，请刷新页面后重试',
      }),
    })
    if (viewDeg) renderer.setViewRotation(viewDeg)
  }
  return renderer
}

/** 本次会话应使用的解码路径：强制软解、或 WebCodecs 不可用（非安全上下文）时走 WASM */
function pickDecoderKind(): 'webcodecs' | 'wasm' {
  if (forcedSoftDecode) return 'wasm'
  return hasCodecsSupport() ? 'webcodecs' : 'wasm'
}

/** 软解跑不动时可改选的方案（按浏览器真实能力列，避免推荐一个本机也没有的选项） */
function decodeAlternatives(): string {
  const opts: string[] = []
  if (hasCodecsSupport()) opts.push('「WebCodecs 硬解」')
  if (hasRTCSupport()) opts.push('「WebRTC」')
  return opts.length ? opts.join(' 或 ') : '其他可用方案'
}

/**
 * 软解啃不动时的失败文案。
 *
 * 这里只失败、不换方案：方案选择权归用户，只有「自动」才允许自动切换
 * （见 resolveMode）。显式选了软解就如实告知能力边界，并指出可改选什么。
 */
function softDecodeFailMsg(profile: number | null): string {
  const codec = profile === null
    ? '当前码流'
    : '设备输出的 ' + (profile === 100 ? 'High' : 'profile ' + profile) + ' 码流'
  return '画面获取失败：' + codec + '超出软解码器能力（仅支持 Baseline profile）。'
    + '请改选' + decodeAlternatives() + '，或把「投屏方案」设为「自动」交给它自动选择'
}

/** 重建解码管线（新会话/切换方案/换流）：丢弃解码器、要求新的配置包 */
function resetPipeline() {
  sessionSeq++
  hwDecoder?.close()
  hwDecoder = null
  softDecoder?.dispose()
  softDecoder = null
  needConfig = true
  configData = null
  decoderKind = ''
  renderer?.newSession()
}

function reportPipeline() {
  setStatus({
    decoderKind,
    renderKind: renderer ? renderer.renderKind : '',
  })
}

/** 解码错误：仍在等首帧则保持 loading 交给超时兜底，否则仅提示 */
function onDecodeError(message: string) {
  if (!waitingKeyframe) showMsg(message)
}

function handleVideoFrame(flags: number, pts: number, payload: Uint8Array) {
  const isConfig = (flags & 1) !== 0
  const isKey = (flags & 2) !== 0

  if (isConfig) {
    configData = payload
    // 配置包（SPS/PPS）决定 codec 串：重建解码器并等待其后的第一个关键帧
    hwDecoder?.close()
    hwDecoder = null
    softDecoder?.dispose()
    softDecoder = null
    needConfig = false
    const kind = pickDecoderKind()
    decoderKind = kind
    if (!ensureRenderer()) return
    // 解码回调逐帧取渲染器：画布可能在会话中途被 React 换掉（切标签页重挂载），
    // 闭包抓住旧实例就会把帧画到已脱离文档的画布上——统计正常但屏幕冻结。
    const paint = (frame: VideoFrame) => {
      firstFrameReady()
      const r = ensureRenderer()
      if (r) r.render(frame) // 所有权移交渲染器（内部 close）
      else frame.close()
    }
    if (kind === 'webcodecs') {
      hwDecoder = new H264Decoder({
        onFrame: paint,
        onError: onDecodeError,
        // 追帧阈值：队列超过 12 帧（~200ms@60fps）时丢 delta 到下一个关键帧
        maxQueue: 12,
      })
      hwDecoder.setConfiguration(configData)
    } else {
      const seq = sessionSeq
      // 预检 SPS profile：h264bsd 只支持 Baseline(66)，多数 Android 机型输出 High(100)。
      // 这里刻意「只失败、不换方案」：用户显式选了软解就尊重这个选择，跑不动就摊牌。
      // profile 解析不到（null）时不拦截，交给运行时检测兜底。
      const profile = spsProfileIdc(configData)
      if (profile !== null && profile !== 66) {
        decoderKind = 'wasm'
        reportPipeline()
        disarmFirstFrameTimeout()
        waitingKeyframe = false
        setStatus({ phase: 'failed', msg: softDecodeFailMsg(profile) })
        return
      }
      // 软解每输出一幅通知首帧就绪（没有 VideoFrame 回调可挂钩）
      const sd = new SoftH264Decoder(ensureRenderer, onDecodeError, {
        onPicture: () => firstFrameReady(),
        // 运行时二次检测（预检放行但仍解不出时）：同样只失败，不替用户换方案
        onUnsupported: () => {
          if (seq !== sessionSeq || softDecoder !== sd) return
          disarmFirstFrameTimeout()
          waitingKeyframe = false
          setStatus({ phase: 'failed', msg: softDecodeFailMsg(configData ? spsProfileIdc(configData) : null) })
        },
      })
      softDecoder = sd
      // wasm 异步加载完成后才真正开始解码；期间到达的帧被丢弃，
      // 等下一个关键帧（GOP=2s）自然接上
      void sd.init().then(() => {
        if (seq === sessionSeq && softDecoder === sd) reportPipeline()
      })
    }
    reportPipeline()
    return
  }

  if (needConfig || (!hwDecoder && !softDecoder)) return // 等首个配置包

  const data = (isKey && configData) ? concat(configData, payload) : payload
  if (hwDecoder) {
    hwDecoder.push({ key: isKey, timestamp: pts, duration: 33333, data })
    return
  }
  if (softDecoder?.ready) softDecoder.push(data, isKey) // wasm 仍在加载时丢弃
}

/** 上一次的设备标题（`设备（宽×高）`）：resize 时只换尺寸，设备名复用这里 */
let lastTitle = ''

// ---------- 顶部标题 ----------
// 标题只承载「设备型号（编码尺寸）」。方案名由页头徽标展示，写进标题就是重复——
// 此前只有 WebRTC 通道在自己的实现里硬编码了【WebRTC】前缀，另外两条路径都没有。

/** meta：设备型号 + 编码尺寸 */
function setTitleMeta(device: string, w: number, h: number) {
  lastTitle = `${device}（${w}×${h}）`
  setStatus({ title: lastTitle })
}

/** resize：只换尺寸（没有 meta 就不写，否则会把设备名弄丢） */
function setTitleSize(w: number, h: number) {
  if (!lastTitle) return
  lastTitle = lastTitle.replace(/（.*）/, `（${w}×${h}）`)
  setStatus({ title: lastTitle })
}

function openVideoWS(serial: string) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const ws = new WebSocket(`${proto}://${location.host}/ws/video/${encodeURIComponent(serial)}`)
  ws.binaryType = 'arraybuffer'
  mirrorWS = ws

  ws.onmessage = ev => {
    if (typeof ev.data === 'string') {
      const msg = JSON.parse(ev.data)
      if (msg.type === 'meta') {
        videoW = msg.width; videoH = msg.height
        setTitleMeta(msg.device, msg.width, msg.height)
      } else if (msg.type === 'resize') {
        videoW = msg.width; videoH = msg.height
        setTitleSize(msg.width, msg.height)
      }
      return
    }
    const buf = new Uint8Array(ev.data)
    if (buf.length < 9) return
    const flags = buf[0]
    const pts = Number(new DataView(buf.buffer, buf.byteOffset).getBigUint64(1))
    const payload = buf.subarray(9)
    if (!(flags & 1)) { frameCount++; byteCount += payload.length }
    tickStats()
    handleVideoFrame(flags, pts, payload)
  }
  ws.onclose = ev => {
    if (mirrorWS !== ws) return
    mirrorWS = null
    if (mirrorSerial) {
      if (waitingKeyframe) {
        waitingKeyframe = false
        disarmFirstFrameTimeout()
        setStatus({ phase: 'failed', msg: '画面获取失败：' + (ev.reason || '投屏流未建立即断开') })
      } else {
        showMsg(ev.reason || '投屏流已结束')
      }
    }
  }
}

function concat(a: Uint8Array, b: Uint8Array) {
  const out = new Uint8Array(a.length + b.length)
  out.set(a); out.set(b, a.length)
  return out
}

// ---------- 音频通道（独立 WS：Opus → AudioDecoder → WSOLA Worklet） ----------
function openAudioWS(serial: string) {
  closeAudio()
  tryAudioConnect(serial, 0)
}

function tryAudioConnect(serial: string, attempt: number) {
  if (mirrorSerial !== serial || attempt > 30) return
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const ws = new WebSocket(`${proto}://${location.host}/ws/audio/${encodeURIComponent(serial)}`)
  ws.binaryType = 'arraybuffer'
  audioWS = ws

  ws.onclose = ev => {
    if (audioWS !== ws) return
    audioWS = null
    // 4003 = 设备端已明确禁用音频（如 audio_dup 需 Android 13+），不再重试
    if (ev.code === 4003) {
      if (mirrorSerial === serial) showMsg('设备端音频不可用（audio_dup 需 Android 13+），已按仅视频继续')
      return
    }
    // 尚未就绪：300ms 重试（采集初始化有 1~2 秒延迟）
    if (mirrorSerial === serial) {
      audioRetryTimer = setTimeout(() => tryAudioConnect(serial, attempt + 1), 300)
    }
  }
  ws.onmessage = ev => {
    if (typeof ev.data === 'string') return // meta（opus）
    const buf = new Uint8Array(ev.data)
    if (buf.length < 9) return
    const flags = buf[0]
    const pts = Number(new DataView(buf.buffer, buf.byteOffset).getBigUint64(1))
    const payload = buf.subarray(9)
    if (flags & 1) { setupAudio(payload); return }
    audioPackets++
    if (audioPackets === 1) audioOn = true
    if (audioDecoder && audioDecoder.state === 'configured') {
      try {
        audioDecoder.decode(new EncodedAudioChunk({ type: 'key', timestamp: pts, data: payload }))
      } catch (e) { console.warn('音频解码异常', e) }
    }
  }
}

function setupAudio(opusHead: Uint8Array) {
  if (audioDecoder) return
  if (!('AudioDecoder' in window)) { showMsg('浏览器不支持 WebCodecs 音频解码'); return }
  const channels = opusHead[9]
  const sampleRate = new DataView(opusHead.buffer, opusHead.byteOffset).getUint32(12, true)
  if (channels < 1 || channels > 8 || sampleRate < 8000) { showMsg('音频头解析失败'); return }

  audioDecoder = new AudioDecoder({
    output: audioData => {
      if (audioNode) pushAudio(audioData)
      else audioData.close()
    },
    error: e => showMsg('音频解码错误: ' + e.message),
  })
  audioDecoder.configure({ codec: 'opus', sampleRate, numberOfChannels: channels, description: opusHead.slice() })

  // AudioWorklet：ya-webadb（Tango）pcm-player 移植——预缓冲 0.1s 起播、
  // 积压 >0.25s WSOLA 0.9× 追帧至 0.15s、>0.35s 丢最旧，延迟稳定在 100~350ms
  const workletCode = `
    const INPUT_HOP_SIZE = 3000;
    const SCALE = 0.9;
    const OUTPUT_HOP_SIZE = (INPUT_HOP_SIZE * SCALE) | 0;
    const WINDOW_SIZE = 6000;
    const WINDOW_WEIGHT_TABLE = new Float32Array(WINDOW_SIZE);
    for (let i = 0; i < WINDOW_SIZE / 2; i++) {
      WINDOW_WEIGHT_TABLE[i] = Math.sin((i / WINDOW_SIZE) * Math.PI);
      WINDOW_WEIGHT_TABLE[WINDOW_SIZE - i - 1] = WINDOW_WEIGHT_TABLE[i];
    }
    class ADBreezePlayer extends AudioWorkletProcessor {
      constructor(options) {
        super();
        this.channelCount = options.outputChannelCount[0];
        this.chunks = [];
        this.chunkCounts = [];
        this.total = 0;
        this.starting = true;
        this.speedUp = false;
        this.readOffset = 0;
        this.inputOffset = 0;
        this.outputOffset = 0;
        this.readBuf = new Float32Array(this.channelCount);
        this.port.onmessage = e => {
          while (this.total > 0.35 * 48000) {
            this.total -= this.chunkCounts.shift();
            this.chunks.shift();
          }
          const planes = e.data.map(b => new Float32Array(b));
          this.chunks.push(planes);
          const n = planes[0].length;
          this.chunkCounts.push(n);
          this.total += n;
          if (!this.speedUp && this.total > 0.25 * 48000) {
            this.speedUp = true;
            this.readOffset = 0;
            this.inputOffset = 0;
            this.outputOffset = 0;
          }
        };
      }
      process(inputs, outputs) {
        const out = outputs[0];
        const frames = out[0].length;
        if (this.starting) {
          if (this.total < 0.1 * 48000) return true;
          this.starting = false;
        }
        if (this.speedUp && this.total < 0.15 * 48000) {
          this.speedUp = false;
          this.starting = true;
        }
        if (this.speedUp) this.renderSpeedUp(out, frames);
        else this.renderPlain(out, frames);
        return true;
      }
      renderPlain(out, frames) {
        let i = 0;
        while (this.chunks.length > 0 && i < frames) {
          const planes = this.chunks[0];
          const avail = planes[0].length;
          let src = 0;
          while (src < avail && i < frames) {
            for (let c = 0; c < out.length && c < planes.length; c++) out[c][i] = planes[c][src];
            src++; i++;
          }
          if (src < avail) {
            for (let c = 0; c < planes.length; c++) this.chunks[0][c] = planes[c].subarray(src);
            this.chunkCounts[0] -= src;
            this.total -= src;
            return;
          }
          this.total -= src;
          this.chunks.shift();
          this.chunkCounts.shift();
        }
      }
      readSample(offset) {
        for (let i = 0; i < this.chunks.length; i++) {
          const len = this.chunkCounts[i];
          if (offset < len) {
            const planes = this.chunks[i];
            for (let c = 0; c < this.readBuf.length && c < planes.length; c++) this.readBuf[c] = planes[c][offset];
            return;
          }
          offset -= len;
        }
        this.readBuf.fill(0);
      }
      renderSpeedUp(out, frames) {
        for (let i = 0; i < frames; i++) {
          let weightSum = 0;
          const firstWindow = Math.max(0, Math.floor((this.outputOffset - WINDOW_SIZE) / OUTPUT_HOP_SIZE) + 1);
          let inWindowIndex = this.outputOffset - firstWindow * OUTPUT_HOP_SIZE;
          let inputIndex = firstWindow * INPUT_HOP_SIZE + inWindowIndex;
          while (inputIndex > 0 && inWindowIndex >= 0) {
            this.readSample(inputIndex - this.readOffset);
            const weight = WINDOW_WEIGHT_TABLE[inWindowIndex];
            for (let c = 0; c < out.length && c < this.readBuf.length; c++) out[c][i] += this.readBuf[c] * weight;
            weightSum += weight;
            inputIndex += INPUT_HOP_SIZE - OUTPUT_HOP_SIZE;
            inWindowIndex -= OUTPUT_HOP_SIZE;
          }
          if (weightSum > 0) {
            for (let c = 0; c < out.length && c < this.readBuf.length; c++) out[c][i] /= weightSum;
          }
          this.outputOffset += 1;
          if (firstWindow > 0) {
            this.outputOffset -= OUTPUT_HOP_SIZE;
            this.readOffset -= INPUT_HOP_SIZE;
            this.inputOffset += (1 - SCALE) * INPUT_HOP_SIZE;
          }
        }
        this.inputOffset += frames;
        if (this.chunks.length > 0 && this.inputOffset >= this.chunkCounts[0]) {
          this.total -= this.chunkCounts[0];
          this.readOffset += this.chunkCounts[0];
          this.inputOffset -= this.chunkCounts[0];
          this.chunks.shift();
          this.chunkCounts.shift();
        }
      }
    }
    registerProcessor('adbreeze-player', ADBreezePlayer);
  `
  const ctx = new AudioContext({ latencyHint: 'interactive', sampleRate })
  audioCtx = ctx
  ctx.onstatechange = () => {
    if (ctx.state === 'suspended') showMsg('浏览器限制了自动播放——点击画面即可恢复声音')
  }
  ctx.resume().catch(() => {})
  const blob = new Blob([workletCode], { type: 'application/javascript' })
  ctx.audioWorklet.addModule(URL.createObjectURL(blob)).then(() => {
    audioNode = new AudioWorkletNode(ctx, 'adbreeze-player', {
      numberOfInputs: 0,
      numberOfOutputs: 1,
      outputChannelCount: [channels],
    })
    audioGain = ctx.createGain()
    audioGain.gain.value = pageMuted ? 0 : 1
    audioNode.connect(audioGain).connect(ctx.destination)
  }).catch(e => showMsg('音频播放初始化失败: ' + e.message))
}

function pushAudio(audioData: AudioData) {
  const n = audioData.numberOfFrames, ch = audioData.numberOfChannels
  const planes: Float32Array[] = []
  if (audioData.copyTo) {
    // 新版 Chrome 移除了 AudioData.data，统一用 copyTo 请求 f32-planar
    for (let c = 0; c < ch; c++) {
      const size = audioData.allocationSize({ planeIndex: c, format: 'f32-planar' })
      const p = new Float32Array(size >> 2)
      audioData.copyTo(p, { planeIndex: c, format: 'f32-planar' })
      planes.push(p)
    }
  } else if (audioData.format === 'f32-planar') {
    // TS 类型库已按新规范移除 AudioData.data，运行时旧浏览器仍可能存在
    const legacy = audioData as unknown as { data: ArrayBuffer }
    const src = new Float32Array(legacy.data)
    for (let c = 0; c < ch; c++) planes.push(src.slice(c * n, (c + 1) * n))
  } else if (audioData.format === 'f32') {
    const src = new Float32Array((audioData as unknown as { data: ArrayBuffer }).data)
    for (let c = 0; c < ch; c++) {
      const p = new Float32Array(n)
      for (let j = 0; j < n; j++) p[j] = src[j * ch + c]
      planes.push(p)
    }
  } else if (audioData.format === 's16') {
    const src = new Int16Array((audioData as unknown as { data: ArrayBuffer }).data)
    for (let c = 0; c < ch; c++) {
      const p = new Float32Array(n)
      for (let j = 0; j < n; j++) p[j] = src[j * ch + c] / 32768
      planes.push(p)
    }
  } else {
    audioData.close()
    return
  }
  audioData.close()
  const buffers = planes.map(p => p.buffer)
  if (audioNode) audioNode.port.postMessage(buffers, buffers)
}

function closeAudio() {
  if (audioRetryTimer) { clearTimeout(audioRetryTimer); audioRetryTimer = null }
  if (audioWS) { audioWS.onclose = null; audioWS.close(); audioWS = null }
  if (audioDecoder) { try { audioDecoder.close() } catch { /* 已关闭 */ } audioDecoder = null }
  if (audioCtx) { audioCtx.close().catch(() => {}); audioCtx = null }
  audioNode = null; audioGain = null; audioPackets = 0; audioOn = false
}

// ---------- WebRTC 方案 ----------
function openCtrlWS(serial: string) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const ws = new WebSocket(`${proto}://${location.host}/ws/video/${encodeURIComponent(serial)}?mode=ctrl`)
  mirrorWS = ws
  ws.onmessage = ev => {
    if (typeof ev.data !== 'string') return
    const msg = JSON.parse(ev.data)
    if (msg.type === 'meta') {
      videoW = msg.width; videoH = msg.height
      setTitleMeta(msg.device, msg.width, msg.height)
    } else if (msg.type === 'resize') {
      videoW = msg.width; videoH = msg.height
      setTitleSize(msg.width, msg.height)
    }
  }
  ws.onclose = ev => {
    if (mirrorWS !== ws) return
    mirrorWS = null
    if (!mirrorSerial) return
    if (!firstFrameShown) {
      // 首帧到达前通道断开：明确转失败，不能停在 loading
      disarmFirstFrameTimeout()
      setStatus({ phase: 'failed', msg: '画面获取失败：' + (ev.reason || 'WebRTC 控制通道断开') })
    } else {
      showMsg(ev.reason || '投屏流已结束')
    }
  }
}

async function startWebrtcViewer(serial: string) {
  if (!hasRTCSupport()) return
  const pc = new RTCPeerConnection()
  webrtcPC = pc
  const a = audioEl!
  // 必须先声明接收轨：否则 createOffer 产出的 SDP 没有任何 m= 行，
  // 浏览器不启动 ICE gathering（offer 无 ice-ufrag），pion 直接拒绝协商
  pc.addTransceiver('video', { direction: 'recvonly' })
  pc.addTransceiver('audio', { direction: 'recvonly' })

  pc.ontrack = ev => {
    if (ev.track.kind === 'video') {
      const v = document.createElement('video')
      v.playsInline = true
      v.muted = true
      v.srcObject = ev.streams && ev.streams[0] ? ev.streams[0] : new MediaStream([ev.track])
      v.play().catch(() => {})
      // 隐藏 video 的合成器会丢帧导致 rVFC 不触发，用 rAF 循环取解码帧；
      // 上屏走统一渲染器（WebGL 时 <video> 直接作为纹理源，零拷贝）
      decoderKind = 'webrtc'
      reportPipeline()
      let drewFirst = false
      const drawLoop = () => {
        if (webrtcPC !== pc) return
        if (v.videoWidth && canvasEl) {
          videoW = v.videoWidth; videoH = v.videoHeight
          const r = ensureRenderer()
          if (r) r.renderVideo(v)
          else canvasEl.getContext('2d')?.drawImage(v, 0, 0, v.videoWidth, v.videoHeight)
          if (!drewFirst) { drewFirst = true; firstFrameReady() } // 首帧真正画上才解除 loading
        }
        requestAnimationFrame(drawLoop)
      }
      requestAnimationFrame(drawLoop)
      // 帧率统计：getStats 的 framesDecoded 增量（隐藏 video 的 totalVideoFrames 不可靠）
      const statsLoop = async () => {
        let prev = 0
        while (webrtcPC === pc) {
          await new Promise(res => setTimeout(res, 1000))
          if (webrtcPC !== pc) return
          const st = await pc.getStats()
          let decoded = 0
          st.forEach(s => { if (s.type === 'inbound-rtp' && s.kind === 'video') decoded = s.framesDecoded })
          if (decoded > prev) clearKeyframeWait()
          // 同 tickStats：方案与渲染后端交给徽标，统计行只报帧率
          setStatus({ stats: `${Math.max(0, decoded - prev)} fps` })
          prev = decoded
        }
      }
      statsLoop()
    } else {
      a.srcObject = ev.streams && ev.streams[0] ? ev.streams[0] : new MediaStream([ev.track])
      a.muted = pageMuted
      a.play().catch(() => {})
    }
  }
  pc.onconnectionstatechange = () => {
    if (webrtcPC !== pc || !mirrorSerial) return
    if (pc.connectionState === 'failed' || pc.connectionState === 'closed') {
      if (!firstFrameShown) {
        // 首帧前连接失败：立即转失败并说明原因（如 ICE 不通），不等 12 秒超时
        disarmFirstFrameTimeout()
        waitingKeyframe = false
        setStatus({ phase: 'failed', msg: '画面获取失败：WebRTC 连接失败（ICE 无法连通，检查网络/防火墙），可重试或改用 WebCodecs 方案' })
      } else {
        showMsg('WebRTC 连接断开')
      }
    } else if (pc.connectionState === 'disconnected') {
      if (!firstFrameShown) {
        disarmFirstFrameTimeout()
        waitingKeyframe = false
        setStatus({ phase: 'failed', msg: '画面获取失败：WebRTC 连接断开' })
      } else {
        showMsg('WebRTC 连接断开')
      }
    }
  }

  const offer = await pc.createOffer()
  await pc.setLocalDescription(offer)
  // 非 trickle：等 ICE gathering 完成再发 offer（内网 host candidate 直连）
  await new Promise<void>(res => {
    if (pc.iceGatheringState === 'complete') return res()
    const t = setTimeout(res, 2000)
    pc.addEventListener('icegatheringstatechange', () => {
      if (pc.iceGatheringState === 'complete') { clearTimeout(t); res() }
    })
  })
  const r = await api<{ type: RTCSessionDescription['type']; sdp: string }>('POST', `/api/devices/${encodeURIComponent(serial)}/webrtc`, {
    sdp: pc.localDescription!.sdp, type: pc.localDescription!.type,
  })
  if (webrtcPC !== pc) { pc.close(); return }
  await pc.setRemoteDescription({ type: r.type, sdp: r.sdp })
}

// ---------- 启停与切换 ----------
function closeViewers() {
  if (mirrorWS) { mirrorWS.onclose = null; mirrorWS.close(); mirrorWS = null }
  resetPipeline()
  closeWebrtcViewer()
  closeAudio()
}
function closeWebrtcViewer() {
  if (webrtcPC) { try { webrtcPC.close() } catch { /* 已关闭 */ } webrtcPC = null }
  if (audioEl) { audioEl.pause(); audioEl.srcObject = null }
}

export function stopMirror(callApi: boolean) {
  disarmFirstFrameTimeout()
  if (mirrorWS) { mirrorWS.onclose = null; mirrorWS.close(); mirrorWS = null }
  resetPipeline()
  closeWebrtcViewer()
  closeAudio()
  if (callApi && mirrorSerial) api('DELETE', `/api/devices/${encodeURIComponent(mirrorSerial)}/mirror`).catch(() => {})
  mirrorSerial = null
  deviceAudio = false
  audioDup = false
  waitingKeyframe = false
  setStatus({ running: false, title: '投屏', stats: '', msg: '', phase: 'loading', decoderKind: '', renderKind: '' })
}

// 观众感知停止：还有其他观看者时仅断开本页，不停止共享流
export async function stopMirrorViewerAware() {
  if (!mirrorSerial) { stopMirror(false); return }
  try {
    const info = await api<{ viewers?: number }>('GET', `/api/devices/${encodeURIComponent(mirrorSerial)}/mirror`)
    if ((info.viewers ?? 1) > 1) {
      stopMirror(false)
      alert('其他页面仍在观看该设备，已仅断开本页（设备端投屏流保持运行）')
      return
    }
  } catch { /* 查询失败按原行为停止 */ }
  stopMirror(true)
}

/**
 * 解析期望方案到实际可用方案（降级优先级：WebCodecs → WebRTC → WASM 软解）。
 * 返回实际方案与降级说明（未降级时为空串）。
 */
function resolveMode(want: MirrorSettings['mode']): { mode: 'codecs' | 'webrtc' | 'wasm'; notice: string } {
  const hasCodecs = hasCodecsSupport()
  const hasRTC = hasRTCSupport()
  const hasSoft = hasSoftDecodeSupport()
  let mode = want
  let notice = ''
  if (mode === 'auto') mode = hasCodecs ? 'codecs' : hasRTC ? 'webrtc' : 'wasm'
  if (mode === 'codecs' && !hasCodecs) {
    if (hasRTC) { mode = 'webrtc'; notice = '当前浏览器不支持 WebCodecs（HTTP 非安全上下文或版本过旧），已自动改用 WebRTC 投屏' }
    else if (hasSoft) { mode = 'wasm'; notice = '当前浏览器不支持 WebCodecs，已自动改用 WASM 软解投屏' }
  } else if (mode === 'webrtc' && !hasRTC) {
    if (hasCodecs) { mode = 'codecs'; notice = '当前浏览器不支持 WebRTC，已自动改用 WebCodecs 投屏' }
    else if (hasSoft) { mode = 'wasm'; notice = '当前浏览器不支持 WebRTC，已改用 WASM 软解投屏' }
  } else if (mode === 'wasm' && !hasSoft) {
    mode = hasCodecs ? 'codecs' : 'webrtc'
    notice = '当前浏览器不支持 WebAssembly，已改用' + (mode === 'codecs' ? ' WebCodecs' : ' WebRTC') + ' 投屏'
  }
  return { mode, notice }
}

/** 建立观看通道（mode 必须已解析）：打开对应的 WS / PeerConnection */
function openViewer(serial: string, mode: 'codecs' | 'webrtc' | 'wasm') {
  forcedSoftDecode = mode === 'wasm'
  webrtcMode = mode === 'webrtc'
  if (webrtcMode) {
    openCtrlWS(serial)
    return startWebrtcViewer(serial)
  }
  openVideoWS(serial)
  if (audioDup || !deviceAudio) openAudioWS(serial)
}

/** 当前会话实际生效的方案（用于判断切换方案是否需要重建观看通道） */
function effectiveMode(): 'codecs' | 'webrtc' | 'wasm' {
  return webrtcMode ? 'webrtc' : decoderKind === 'wasm' ? 'wasm' : 'codecs'
}

export async function startMirror(serial: string) {
  const s = loadMirrorSettings(serial)
  if (!hasCodecsSupport() && !hasRTCSupport() && !hasSoftDecodeSupport()) {
    alert('当前浏览器不支持 WebCodecs、WebRTC 与 WebAssembly，无法投屏。'
      + '请使用 Chrome / Edge 94+ 或较新的 Safari'
      + (codecsBlockedByInsecureContext() ? '\n（当前为 HTTP 非安全上下文访问：WebCodecs 被浏览器禁用，可改用 WebRTC / WASM 软解，或用 localhost 访问、为服务配置 HTTPS 反向代理）' : ''))
    return
  }
  // 一个标签页只投一台：切换设备前先干净结束当前会话（不删服务端流）
  if (mirrorSerial) stopMirror(false)
  mirrorSerial = serial
  deviceAudio = s.audioMode === 'device'
  audioDup = s.audioMode === 'both'
  videoW = 720; videoH = 1280

  const { mode, notice } = resolveMode(s.mode || 'auto')
  setStatus({ running: true, title: serial + ' — 连接中…', stats: '', phase: 'loading', decoderKind: '', renderKind: '' })
  showMsg(notice)
  // 清掉上一次的画面：等待新首帧前不能让旧帧造成"已在投"的错觉
  resetPipeline()
  canvasEl?.focus()
  try {
    await api('POST', `/api/devices/${encodeURIComponent(serial)}/mirror`, {
      audioMode: s.audioMode, bitrateMbps: s.bitrateMbps, shortSize: s.shortSize, maxFps: s.maxFps,
    })
    openViewer(serial, mode)
    armFirstFrameTimeout() // 启动请求成功但迟迟无首帧 → 超时失败
  } catch (e) {
    setStatus({ phase: 'failed', msg: '画面获取失败：启动投屏失败 ' + (e as Error).message })
  }
}

// 设置应用：仅切方案 → 轻量重建观看通道；画质/音频变更 → 重启设备端流
export async function applySettings(serial: string, next: MirrorSettings) {
  if (!mirrorSerial || mirrorRestarting || mirrorSerial !== serial) return
  const { mode } = resolveMode(next.mode)
  const qualityChanged = qualityJSON(next) !== savedQualityJSON(serial)
  const modeChanged = mode !== effectiveMode()
  saveMirrorSettings(serial, next)
  if (!qualityChanged && !modeChanged) return
  if (!qualityChanged) {
    mirrorRestarting = true
    // 切换方案：设备端流不动，只重建观看通道
    waitKeyframe()
    try {
      closeViewers()
      openViewer(serial, mode)
    } catch (e) {
      setStatus({ phase: 'failed', msg: '画面获取失败：切换投屏方案失败 ' + (e as Error).message })
    } finally {
      mirrorRestarting = false
    }
    return
  }
  await restartStream()
}

async function restartStream() {
  const serial = mirrorSerial
  if (!serial || mirrorRestarting) return
  mirrorRestarting = true
  // 参数切换（码率/分辨率/帧率/声音）：清画面 + loading，等新参数流首帧
  setStatus({ phase: 'loading', msg: '正在应用新参数…' })
  try {
    closeViewers()
    await api('DELETE', `/api/devices/${encodeURIComponent(serial)}/mirror`).catch(() => {})
    const s = loadMirrorSettings(serial)
    const { mode } = resolveMode(s.mode)
    await api('POST', `/api/devices/${encodeURIComponent(serial)}/mirror`, {
      audioMode: s.audioMode, bitrateMbps: s.bitrateMbps, shortSize: s.shortSize, maxFps: s.maxFps,
    })
    deviceAudio = s.audioMode === 'device'
    audioDup = s.audioMode === 'both'
    waitKeyframe()
    openViewer(serial, mode)
  } catch (e) {
    setStatus({ phase: 'failed', msg: '画面获取失败：重启投屏流失败 ' + (e as Error).message })
  } finally {
    mirrorRestarting = false
  }
}

// ---------- 页面静音 ----------
export function setPageMuted(m: boolean) {
  pageMuted = m
  if (audioGain) audioGain.gain.value = m ? 0 : 1
  if (audioEl) audioEl.muted = m
  setStatus({ muted: m })
}
export function isMuted() { return pageMuted }

// ---------- 剪贴板 ----------
export async function clipboardGet(serial: string): Promise<string> {
  const r = await api<{ text: string }>('GET', `/api/devices/${encodeURIComponent(serial)}/clipboard`)
  return r.text || ''
}
export async function clipboardSet(serial: string, text: string, paste: boolean) {
  await api('POST', `/api/devices/${encodeURIComponent(serial)}/clipboard`, { text, paste })
}

// ---------- 键盘映射 ----------
const KEYMAP: Record<string, number> = {
  Enter: 66, Backspace: 67, Tab: 61, Escape: 111,
  ArrowUp: 19, ArrowDown: 20, ArrowLeft: 21, ArrowRight: 22,
  Home: 122, End: 123, PageUp: 92, PageDown: 93,
  Delete: 112, Insert: 124, VolumeUp: 24, VolumeDown: 25,
}
function androidKeyFor(key: string): number | null {
  if (key in KEYMAP) return KEYMAP[key]
  if (key.length === 1) {
    const c = key
    if (c >= 'a' && c <= 'z') return 29 + c.charCodeAt(0) - 97
    if (c >= 'A' && c <= 'Z') return 29 + c.charCodeAt(0) - 65
    if (c >= '0' && c <= '9') return 7 + Number(c)
    if (c === ' ') return 62
    if (c === '@') return 77
    if (c === '*') return 17
    if (c === '#') return 18
  }
  return null
}
export { androidKeyFor }

