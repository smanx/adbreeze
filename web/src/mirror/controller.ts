// 投屏控制器：从 legacy 页完整迁移的逻辑（单例，一个标签页一条活跃观看会话）。
// 保留所有既有修复：双方案降级、iOS touch、观众感知停止、WSOLA 音频、
// SPS 级别补丁（服务端）、固定 20ms RTP（服务端）、按设备记忆设置。
//
// 解码/渲染层移植自 ya-webadb（Tango）：
//   * WebCodecs 封装带回放缓冲、解码器回收/硬解故障恢复与统计埋点（decoder.ts）
//   * WebGL 上屏 + 着色器缩放 + rAF 帧门控 + ResizeObserver 重绘（render.ts）
//   * h264bsd WASM 软解兜底，可在 HTTP 非安全上下文使用（soft-decoder.ts）
import { api, wsUrl } from '../api'
import { t } from '../i18n'
import { FrameRenderer, type RenderKind } from './render'
import { H264Decoder, spsProfileIdc } from './decoder'
import { SoftH264Decoder, hasWebAssembly } from './soft-decoder'
import { createOpusWasmDecoder, type OpusWasmDecoder } from './opus-wasm'

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
  // 但「是否需要软件编码器」要纳入：切到软解时设备端编码器必须换成软件编码器
  // （否则码流是 h264bsd 啃不动的 High 档），属编码器参数变更，必须重启流，
  // 不能像普通切方案那样只重建观看通道。
  return JSON.stringify([s.audioMode, s.bitrateMbps, s.shortSize, s.maxFps, needsSoftwareEncoder(s.mode)])
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
  /** 链路延迟明细（悬停统计行显示成分段列表；WS 与 WebRTC 口径不同） */
  latency?: LatencyInfo | null
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
/** libopus WASM 兜底解码器（非安全上下文下 AudioDecoder 不可用时启用） */
let wasmOpus: OpusWasmDecoder | null = null
let audioCtx: AudioContext | null = null
let audioNode: AudioWorkletNode | null = null
let audioGain: GainNode | null = null
let audioRetryTimer: ReturnType<typeof setTimeout> | null = null
let audioPackets = 0
let audioOn = false
/** 音频通道是否已初始化（WebCodecs 或 WASM 二选一），防止重复配置 */
let audioReady = false
/** 音频会话代际：WASM 异步加载完成后据此判断会话是否已失效 */
let audioSeq = 0
let pageMuted = false
let mirrorRestarting = false

let frameCount = 0
let byteCount = 0
let lastTick = 0
let configData: Uint8Array | null = null
/** 最近一次对外广播的状态（含初始空值），供组件重挂载时回放 */
let lastStatus: Partial<MirrorStatus> = {}

// ---------- 链路延迟（悬停统计行出分段列表，按秒平均） ----------
// 全链路共 8 段，按「同一时钟域内可拿到的时刻」划分可测性：
//   ① 设备屏幕合成/采集   设备 SurfaceFlinger 内部，无时间戳出口   ❌
//   ② 设备 H.264 编码     设备 MediaCodec 内部                    ❌
//   ③ 设备端打包           设备 scrcpy server                     ❌
//   ④ adb 隧道（设备→主机）PTS 为设备单调时钟，与墙钟不同源         ❌
//      （①②③④ 合计通常 10~40ms+，视 USB/WiFi 与编码器而定）
//   ⑤ 后端扇出（读→写）   RecvWall→SendWall，同为后端时钟，免校准 ✅
//   ⑥ 网络+派发            SendWall→浏览器收到，跨双端墙钟          ✅（需时钟校准）
//   ⑦ 解码（含排队）       浏览器收到→解码输出                     ✅
//   ⑧ 上屏提交+合成等待    渲染提交→下一个 rAF（合成器取走画布）     ✅
// 时钟校准：WS 上跑 NTP 式 ping/pong（浏览器 t0 → 后端 t1/t2 → 浏览器 t3），
// offset=((t1-t0)+(t2-t3))/2，误差约 RTT/2（局域网 1~3ms）；不校准的话⑥测出来
// 的是两台机器的时钟差而不是延迟。
const lat = { fSum: 0, fN: 0, nSum: 0, nN: 0, dSum: 0, dN: 0, rSum: 0, rN: 0, cSum: 0, cN: 0 }
/** PTS → 浏览器收到时刻：解码输出回调按 frame.timestamp 精确配对（WebCodecs 路径） */
const arrivalByPts = new Map<number, number>()
/** performance.now() 折成 Unix 毫秒的基准（老浏览器无 timeOrigin 时回退估算一次） */
const timeOriginMs = performance.timeOrigin ?? (Date.now() - performance.now())
/** 时钟偏移：后端时钟 − 浏览器时钟（ms）；null = 尚未校准（⑥ 的结果不可信） */
let clockOffsetMs: number | null = null
/** 最近一次 ping/pong 的 RTT（ms），用于标注校准误差 */
let clockRttMs: number | null = null
/** 渲染提交序号：只给「最后一帧」计合成等待（中间帧被覆盖，从未上屏） */
let renderSeq = 0

/** 悬停统计行显示的延迟明细（结构化，供界面渲染成列表） */
export interface LatencyRow { label: string; value: string }
export interface LatencyInfo {
  /** 标题行：口径说明（含时钟校准状态） */
  title: string
  /** 全链路各段（按链路顺序；不可测的段也列出，值标「无法计算」） */
  rows: LatencyRow[]
  /** 可检测合计（仅各段可加时给出；WebRTC 口径不可加，不设） */
  total?: string
  note?: string
}

/** 全链路 8 段的列表行标签（WS 与 WebRTC 共用同一编号；语言切换时随取随译） */
const stage = () => ({
  capture: t('lat.stage.capture'),
  encode: t('lat.stage.encode'),
  pack: t('lat.stage.pack'),
  adb: t('lat.stage.adb'),
  fanout: t('lat.stage.fanout'),
  net: t('lat.stage.net'),
  decode: t('lat.stage.decode'),
  submit: t('lat.stage.submit'),
  composite: t('lat.stage.composite'),
})
/** 不可测段的值：设备侧无时间戳出口 / 时钟不同源，就地标注、不删行 */
const unmeasurable = () => t('lat.unmeasurable')

function latReset() {
  lat.fSum = lat.fN = lat.nSum = lat.nN = lat.dSum = lat.dN = lat.rSum = lat.rN = lat.cSum = lat.cN = 0
  arrivalByPts.clear()
  clockOffsetMs = null
  clockRttMs = null
  renderSeq = 0
}

/** 渲染提交后测合成等待：下一个 rAF 即合成器取走画布内容的时刻（0~一个刷新周期） */
function measureComposite(submitAt: number) {
  const my = ++renderSeq
  requestAnimationFrame(() => {
    if (my !== renderSeq) return // 已被后续帧覆盖：这次提交从未上屏，不计
    lat.cSum += performance.now() - submitAt
    lat.cN++
  })
}

/** 收到 pong：NTP 式偏移估计；RTT 过大的样本（调度抖动）弃用 */
function onClockPong(t0: number, t1: number, t2: number) {
  const t3 = Date.now()
  const rtt = (t3 - t0) - (t2 - t1)
  if (rtt < 0 || rtt > 100) return
  const offset = ((t1 - t0) + (t2 - t3)) / 2
  clockOffsetMs = clockOffsetMs === null ? offset : clockOffsetMs * 0.5 + offset * 0.5
  clockRttMs = rtt
}

/** 生成统计行悬停的延迟列表（WS 路径）；样本不足返回 null（不显示） */
function latInfo(): LatencyInfo | null {
  if (lat.fN === 0 && lat.dN === 0) return null
  const avg = (sum: number, n: number) => (n > 0 ? Math.round(sum / n) + 'ms' : '—')
  const mean = (sum: number, n: number) => (n > 0 ? sum / n : 0)
  const calib = clockOffsetMs === null
    ? t('lat.clockUncalibrated')
    : t('lat.clockError', { n: Math.max(1, Math.round((clockRttMs ?? 0) / 2)) })
  // 完整 8 段全列：①~④ 位于设备侧不可测（就地标注，不删行）
  const rows: LatencyRow[] = [
    { label: stage().capture, value: unmeasurable() },
    { label: stage().encode, value: unmeasurable() },
    { label: stage().pack, value: unmeasurable() },
    { label: stage().adb, value: unmeasurable() },
    { label: stage().fanout, value: avg(lat.fSum, lat.fN) },
    { label: stage().net, value: avg(lat.nSum, lat.nN) },
  ]
  let total = mean(lat.fSum, lat.fN) + mean(lat.nSum, lat.nN)
  if (decoderKind === 'wasm') {
    rows.push({ label: t('lat.fused'), value: avg(lat.dSum, lat.dN) })
    total += mean(lat.dSum, lat.dN)
  } else {
    rows.push({ label: stage().decode, value: avg(lat.dSum, lat.dN) })
    rows.push({ label: stage().submit, value: avg(lat.rSum, lat.rN) })
    total += mean(lat.dSum, lat.dN) + mean(lat.rSum, lat.rN)
  }
  rows.push({ label: stage().composite, value: avg(lat.cSum, lat.cN) })
  total += mean(lat.cSum, lat.cN)
  return {
    title: t('lat.title', { calib }),
    rows,
    total: Math.round(total) + 'ms',
    note: t('lat.noteDevice'),
  }
}

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
  showMsg(t('mirror.overlayLoading'))
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
        ? t('mirror.failWebrtcTimeout')
        : t('mirror.failTimeout'),
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
      if (dropDelta > 0) parts.push(t('stats.drop', { n: dropDelta }))
      const q = hwDecoder?.decodeQueueSize ?? 0
      if (q > 3) parts.push(t('stats.backlog', { n: q }))
    }
    setStatus({ stats: parts.join(' · '), latency: latInfo() })
    latReset()
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
        msg: t('mirror.failGpuLost'),
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
  if (hasCodecsSupport()) opts.push(t('mirror.altCodecs'))
  if (hasRTCSupport()) opts.push('「WebRTC」')
  return opts.length ? opts.join(t('mirror.altJoin')) : t('mirror.altOthers')
}

/**
 * 软解啃不动时的失败文案。
 *
 * 这里只失败、不换方案：方案选择权归用户，只有「自动」才允许自动切换
 * （见 resolveMode）。显式选了软解就如实告知能力边界，并指出可改选什么。
 */
function softDecodeFailMsg(profile: number | null): string {
  const codec = profile === null
    ? t('mirror.currentStream')
    : t('mirror.deviceStream', { profile: profile === 100 ? 'High' : 'profile ' + profile })
  return t('mirror.failWasmProfile', { codec, alts: decodeAlternatives() })
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
  latReset()
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

function handleVideoFrame(flags: number, pts: number, recvWall: number, sendWall: number, payload: Uint8Array) {
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
      const outAt = performance.now()
      const arrival = arrivalByPts.get(frame.timestamp)
      if (arrival !== undefined) {
        // 解码：浏览器收到 → 输出（含解码器排队）
        arrivalByPts.delete(frame.timestamp)
        lat.dSum += outAt - arrival
        lat.dN++
      }
      const r = ensureRenderer()
      if (r) r.render(frame) // 所有权移交渲染器（内部 close）
      else frame.close()
      // 上屏提交：渲染调用本身（合成在下一 rAF，由 measureComposite 另计）
      lat.rSum += performance.now() - outAt
      lat.rN++
      measureComposite(performance.now())
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

  // 传输两段：后端扇出与网络+派发（口径见文件头延迟注释）。
  // 扇出 = SendWall − RecvWall，同为后端时钟，相减不受双端时钟差影响；
  // 网络 = 浏览器墙钟 − (SendWall − 时钟偏移)，偏移由 ping/pong 校准，
  // 未校准时结果含时钟差（文案会标注）。
  const now = performance.now()
  const nowWall = now + timeOriginMs
  lat.fSum += (sendWall - recvWall) / 1000
  lat.fN++
  const net = nowWall - (sendWall / 1000 - (clockOffsetMs ?? 0))
  if (net >= 0 && net < 2000) { lat.nSum += net; lat.nN++ } // 超范围视为异常样本，弃用

  const data = (isKey && configData) ? concat(configData, payload) : payload
  if (hwDecoder) {
    arrivalByPts.set(pts, now) // 供输出回调按 PTS 配对，算解码段耗时
    if (arrivalByPts.size > 64) arrivalByPts.clear() // 防御：解码器丢帧致配对项残留
    hwDecoder.push({ key: isKey, timestamp: pts, duration: 33333, data })
    return
  }
  if (softDecoder?.ready) {
    // WASM 同步管线：解码与上屏融合计量
    const t0 = performance.now()
    softDecoder.push(data, isKey)
    lat.dSum += performance.now() - t0
    lat.dN++
    measureComposite(performance.now())
  }
}

/** 上一次的设备标题（`设备（宽×高）`）：resize 时只换尺寸，设备名复用这里 */
let lastTitle = ''

// ---------- 顶部标题 ----------
// 标题只承载「设备型号（编码尺寸）」。方案名由页头徽标展示，写进标题就是重复——
// 此前只有 WebRTC 通道在自己的实现里硬编码了【WebRTC】前缀，另外两条路径都没有。

/** meta：设备型号 + 编码尺寸 */
function setTitleMeta(device: string, w: number, h: number) {
  lastTitle = t('mirror.titleWithSize', { device, w, h })
  setStatus({ title: lastTitle })
}

/** resize：只换尺寸（没有 meta 就不写，否则会把设备名弄丢） */
function setTitleSize(w: number, h: number) {
  if (!lastTitle) return
  // 括号内整体替换（中英文括号都匹配）：只换尺寸，设备名复用
  lastTitle = lastTitle.replace(/（.*）|\(.*\)/, t('mirror.titleWithSize', { device: lastTitle.replace(/（.*）|\(.*\)/, ''), w, h }))
  setStatus({ title: lastTitle })
}

function openVideoWS(serial: string) {
  const ws = new WebSocket(wsUrl(`/ws/video/${encodeURIComponent(serial)}`))
  ws.binaryType = 'arraybuffer'
  mirrorWS = ws

  // 时钟校准：NTP 式 ping/pong（见 onClockPong）。首帧立即发一次，
  // 之后每 3s 重估（时钟会漂移，且首次样本可能遇到调度抖动）
  const ping = () => { try { ws.send(JSON.stringify({ type: 'ping', t0: Date.now() })) } catch { /* 已断 */ } }
  ping()
  const pingTimer = setInterval(ping, 3000)

  ws.onmessage = ev => {
    if (typeof ev.data === 'string') {
      const msg = JSON.parse(ev.data)
      if (msg.type === 'meta') {
        videoW = msg.width; videoH = msg.height
        setTitleMeta(msg.device, msg.width, msg.height)
      } else if (msg.type === 'resize') {
        videoW = msg.width; videoH = msg.height
        setTitleSize(msg.width, msg.height)
      } else if (msg.type === 'pong') {
        onClockPong(msg.t0, msg.t1, msg.t2) // NTP 式时钟校准（延迟测量去掉双端时钟差）
      }
      return
    }
    const buf = new Uint8Array(ev.data)
    if (buf.length < 25) return // [1B flags][8B PTS][8B RecvWall][8B SendWall][payload]
    const flags = buf[0]
    const view = new DataView(buf.buffer, buf.byteOffset)
    const pts = Number(view.getBigUint64(1))
    const recvWall = Number(view.getBigUint64(9))
    const sendWall = Number(view.getBigUint64(17))
    const payload = buf.subarray(25)
    if (!(flags & 1)) { frameCount++; byteCount += payload.length }
    tickStats()
    handleVideoFrame(flags, pts, recvWall, sendWall, payload)
  }
  ws.onclose = ev => {
    clearInterval(pingTimer)
    if (mirrorWS !== ws) return
    mirrorWS = null
    if (mirrorSerial) {
      if (waitingKeyframe) {
        waitingKeyframe = false
        disarmFirstFrameTimeout()
        setStatus({ phase: 'failed', msg: t('mirror.failPrefix') + (ev.reason || t('mirror.streamClosedEarly')) })
      } else {
        showMsg(ev.reason || t('mirror.streamEnded'))
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
  const ws = new WebSocket(wsUrl(`/ws/audio/${encodeURIComponent(serial)}`))
  ws.binaryType = 'arraybuffer'
  audioWS = ws

  ws.onclose = ev => {
    if (audioWS !== ws) return
    audioWS = null
    // 4003 = 设备端已明确禁用音频（如 audio_dup 需 Android 13+），不再重试
    if (ev.code === 4003) {
      if (mirrorSerial === serial) showMsg(t('mirror.audioUnavailable'))
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
    } else if (wasmOpus) {
      // WASM 兜底路径：同步解码单个 Opus 帧，输出 PCM 直接给工作器
      try {
        const { channelData, samplesDecoded } = wasmOpus.decodeFrame(payload)
        if (samplesDecoded > 0) postPCM(channelData)
      } catch { /* 单帧损坏：跳过，等后续帧 */ }
    }
  }
}

function setupAudio(opusHead: Uint8Array) {
  if (audioReady) return
  const channels = opusHead[9]
  const sampleRate = new DataView(opusHead.buffer, opusHead.byteOffset).getUint32(12, true)
  if (channels < 1 || channels > 8 || sampleRate < 8000) { showMsg(t('mirror.audioHeaderFail')); return }
  audioReady = true

  // 优先 WebCodecs：硬件/低延迟路径（仅在安全上下文可用）
  if ('AudioDecoder' in window) {
    audioDecoder = new AudioDecoder({
      output: audioData => {
        if (audioNode) pushAudioData(audioData)
        else audioData.close()
      },
      error: e => showMsg(t('mirror.audioDecodeErr') + e.message),
    })
    audioDecoder.configure({ codec: 'opus', sampleRate, numberOfChannels: channels, description: opusHead.slice() })
    setupAudioWorklet(channels, sampleRate)
    return
  }

  // 兜底：非安全上下文（HTTP + IP）没有 AudioDecoder，改用 libopus WASM 解码。
  // 输出同样是 Float32 PCM，喂进同一条 AudioWorklet 播放管线。
  if (!hasWebAssembly()) { showMsg(t('mirror.noWasmAudio')); return }
  const seq = audioSeq
  setupAudioWorklet(channels, sampleRate)
  void createOpusWasmDecoder(opusHead).then(dec => {
    // 等待期间会话可能已结束/切换：丢弃迟到的解码器
    if (seq !== audioSeq || audioDecoder || wasmOpus) { dec?.free(); return }
    if (!dec) { showMsg(t('mirror.audioWasmLoadFail')); return }
    wasmOpus = dec
    // 排空加载期间缓存的帧：解码器就绪前收到的包无法解，等后续帧自然接上即可
    audioPackets = 0
  })
}

/** 建立 AudioWorklet 播放管线（WebCodecs 与 WASM 两条解码路径共用） */
function setupAudioWorklet(channels: number, sampleRate: number) {
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
    if (ctx.state === 'suspended') showMsg(t('mirror.autoplayBlocked'))
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
  }).catch(e => showMsg(t('mirror.audioInitFail') + e.message))
}

/** WebCodecs 路径：把 AudioData 转成按声道分离的 Float32，交给工作器播放 */
function pushAudioData(audioData: AudioData) {
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
  postPCM(planes)
}

/** 把按声道分离的 Float32 PCM 交给工作器播放（两条解码路径共用） */
function postPCM(planes: Float32Array[]) {
  const buffers = planes.map(p => p.buffer)
  if (audioNode) audioNode.port.postMessage(buffers, buffers)
}

function closeAudio() {
  audioSeq++ // 使迟到的 WASM 解码器加载结果失效
  if (audioRetryTimer) { clearTimeout(audioRetryTimer); audioRetryTimer = null }
  if (audioWS) { audioWS.onclose = null; audioWS.close(); audioWS = null }
  if (audioDecoder) { try { audioDecoder.close() } catch { /* 已关闭 */ } audioDecoder = null }
  if (wasmOpus) { try { wasmOpus.free() } catch { /* 已释放 */ } wasmOpus = null }
  if (audioCtx) { audioCtx.close().catch(() => {}); audioCtx = null }
  audioNode = null; audioGain = null; audioPackets = 0; audioOn = false; audioReady = false
}

// ---------- WebRTC 方案 ----------
function openCtrlWS(serial: string) {
  const ws = new WebSocket(wsUrl(`/ws/video/${encodeURIComponent(serial)}?mode=ctrl`))
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
      setStatus({ phase: 'failed', msg: t('mirror.failPrefix') + (ev.reason || t('mirror.failWebrtcCtrlClosed')) })
    } else {
      showMsg(ev.reason || t('mirror.streamEnded'))
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
          // 上屏提交耗时（WebRTC 路径唯一可测的浏览器内段；合成在 rAF 内紧跟，≈0）
          const t0 = performance.now()
          const r = ensureRenderer()
          if (r) r.renderVideo(v)
          else canvasEl.getContext('2d')?.drawImage(v, 0, 0, v.videoWidth, v.videoHeight)
          lat.rSum += performance.now() - t0
          lat.rN++
          if (!drewFirst) { drewFirst = true; firstFrameReady() } // 首帧真正画上才解除 loading
        }
        requestAnimationFrame(drawLoop)
      }
      requestAnimationFrame(drawLoop)
      // 帧率/码率统计：getStats 的 framesDecoded 与 bytesReceived 均为累计值，
      // 取每秒增量（隐藏 video 的 totalVideoFrames 不可靠，故不用它）。
      // WS 路径的 Mbps 来自帧载荷累计（openVideoWS），这里来自 RTP 层累计，
      // 两者相差一个 RTP 头开销（<2%），显示口径一致。
      const statsLoop = async () => {
        let prevFrames = 0
        let prevBytes = 0
        let prevJbd = 0 // jitterBufferDelay 累计值（秒）
        while (webrtcPC === pc) {
          await new Promise(res => setTimeout(res, 1000))
          if (webrtcPC !== pc) return
          const st = await pc.getStats()
          let decoded = 0
          let bytes = 0
          let jbd = 0    // jitterBufferDelay：帧在抖动缓冲累计停留（秒）
          let jitter = 0 // 网络抖动（秒）
          st.forEach(s => {
            if (s.type === 'inbound-rtp' && s.kind === 'video') {
              decoded = s.framesDecoded ?? decoded
              bytes = s.bytesReceived ?? bytes
              jbd = s.jitterBufferDelay ?? jbd
              jitter = s.jitter ?? jitter
            }
          })
          if (decoded > prevFrames) clearKeyframeWait()
          const fps = Math.max(0, decoded - prevFrames)
          // 累计值异常回退（统计重置/换轨）时按 0 计，避免负码率
          const dBytes = bytes >= prevBytes ? bytes - prevBytes : 0
          const mbps = (dBytes * 8 / 1e6).toFixed(1)
          // 同 tickStats：方案与渲染后端交给徽标，统计行只报帧率+码率。
          // 延迟明细：RTP 没有逐帧打戳通道，④~⑦ 只能合并报抖动缓冲深度
          // （缓冲按累计秒/帧数增量摊平）；⑧ 上屏提交可测，合成在 rAF 内紧跟（≈0）。
          // 合计 = 抖动缓冲 + 上屏提交；网络抖动是波动量，不计入合计。
          const dFrames = decoded - prevFrames
          const bufMs = dFrames > 0 && jbd >= prevJbd ? Math.round((jbd - prevJbd) / dFrames * 1000) : null
          const submitMs = lat.rN > 0 ? lat.rSum / lat.rN : 0
          const latency: LatencyInfo = {
            title: t('lat.titleRtp'),
            rows: [
              { label: stage().capture, value: unmeasurable() },
              { label: stage().encode, value: unmeasurable() },
              { label: stage().pack, value: unmeasurable() },
              { label: stage().adb, value: t('lat.mergedJitter') },
              { label: stage().fanout, value: t('lat.mergedJitter') },
              { label: stage().net, value: t('lat.mergedJitter') },
              { label: stage().decode, value: t('lat.mergedJitter') },
              { label: stage().submit, value: lat.rN > 0 ? Math.round(submitMs) + 'ms' : '—' },
              { label: stage().composite, value: t('lat.approxZero') },
              { label: t('lat.jitterBuffer'), value: bufMs === null ? '—' : bufMs + 'ms' },
              { label: t('lat.networkJitter'), value: Math.round(jitter * 1000) + 'ms' },
            ],
            total: bufMs === null ? undefined : Math.round(bufMs + submitMs) + 'ms',
            note: t('lat.noteRtp'),
          }
          setStatus({ stats: `${fps} fps · ${mbps} Mbps`, latency })
          prevFrames = decoded
          prevBytes = bytes
          prevJbd = jbd
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
        setStatus({ phase: 'failed', msg: t('mirror.failWebrtcIce') })
      } else {
        showMsg(t('mirror.webrtcDisconnected'))
      }
    } else if (pc.connectionState === 'disconnected') {
      if (!firstFrameShown) {
        disarmFirstFrameTimeout()
        waitingKeyframe = false
        setStatus({ phase: 'failed', msg: t('mirror.failPrefix') + t('mirror.webrtcDisconnected') })
      } else {
        showMsg(t('mirror.webrtcDisconnected'))
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
  setStatus({ running: false, title: t('mirror.title'), stats: '', msg: '', phase: 'loading', decoderKind: '', renderKind: '', latency: null })
}

// 观众感知停止：还有其他观看者时仅断开本页，不停止共享流
export async function stopMirrorViewerAware() {
  if (!mirrorSerial) { stopMirror(false); return }
  try {
    const info = await api<{ viewers?: number }>('GET', `/api/devices/${encodeURIComponent(mirrorSerial)}/mirror`)
    if ((info.viewers ?? 1) > 1) {
      stopMirror(false)
      alert(t('mirror.othersWatching'))
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
    if (hasRTC) { mode = 'webrtc'; notice = t('mirror.fallbackWebrtc') }
    else if (hasSoft) { mode = 'wasm'; notice = t('mirror.fallbackWasm') }
  } else if (mode === 'webrtc' && !hasRTC) {
    if (hasCodecs) { mode = 'codecs'; notice = t('mirror.fallbackCodecs') }
    else if (hasSoft) { mode = 'wasm'; notice = t('mirror.fallbackWasm2') }
  } else if (mode === 'wasm' && !hasSoft) {
    mode = hasCodecs ? 'codecs' : 'webrtc'
    notice = t('mirror.fallbackNoWasm', { plan: mode === 'codecs' ? 'WebCodecs' : 'WebRTC' })
  }
  return { mode, notice }
}

/**
 * 该设置下是否会走 WASM 软解（进而需要设备端软件编码器）。
 * 必须按 resolveMode 解析后的实际方案判断：「自动」在无 WebCodecs 时也会落到软解，
 * 那时同样得换软编，否则码流是软解啃不动的 High 档。
 */
function needsSoftwareEncoder(mode: MirrorSettings['mode']): boolean {
  return resolveMode(mode).mode === 'wasm'
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
    alert(t('mirror.noSupportAtAll', { httpNote: codecsBlockedByInsecureContext() ? t('mirror.insecureHttpNote') : '' }))
    return
  }
  // 一个标签页只投一台：切换设备前先干净结束当前会话（不删服务端流）
  if (mirrorSerial) stopMirror(false)
  mirrorSerial = serial
  deviceAudio = s.audioMode === 'device'
  audioDup = s.audioMode === 'both'
  videoW = 720; videoH = 1280

  const { mode, notice } = resolveMode(s.mode || 'auto')
  setStatus({ running: true, title: t('mirror.connecting', { serial }), stats: '', phase: 'loading', decoderKind: '', renderKind: '', latency: null })
  showMsg(notice)
  // 清掉上一次的画面：等待新首帧前不能让旧帧造成"已在投"的错觉
  resetPipeline()
  canvasEl?.focus()
  try {
    await api('POST', `/api/devices/${encodeURIComponent(serial)}/mirror`, {
      audioMode: s.audioMode, bitrateMbps: s.bitrateMbps, shortSize: s.shortSize, maxFps: s.maxFps,
      softwareEncoder: needsSoftwareEncoder(s.mode),
    })
    openViewer(serial, mode)
    armFirstFrameTimeout() // 启动请求成功但迟迟无首帧 → 超时失败
  } catch (e) {
    setStatus({ phase: 'failed', msg: t('mirror.failStart', { msg: (e as Error).message }) })
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
      setStatus({ phase: 'failed', msg: t('mirror.failSwitch', { msg: (e as Error).message }) })
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
  setStatus({ phase: 'loading', msg: t('mirror.applying') })
  try {
    closeViewers()
    await api('DELETE', `/api/devices/${encodeURIComponent(serial)}/mirror`).catch(() => {})
    const s = loadMirrorSettings(serial)
    const { mode } = resolveMode(s.mode)
    await api('POST', `/api/devices/${encodeURIComponent(serial)}/mirror`, {
      audioMode: s.audioMode, bitrateMbps: s.bitrateMbps, shortSize: s.shortSize, maxFps: s.maxFps,
      softwareEncoder: needsSoftwareEncoder(s.mode),
    })
    deviceAudio = s.audioMode === 'device'
    audioDup = s.audioMode === 'both'
    waitKeyframe()
    openViewer(serial, mode)
  } catch (e) {
    setStatus({ phase: 'failed', msg: t('mirror.failRestart', { msg: (e as Error).message }) })
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

