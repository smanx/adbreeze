// 投屏控制器：从 legacy 页完整迁移的逻辑（单例，一个标签页一条活跃观看会话）。
// 保留所有既有修复：双方案降级、iOS touch、观众感知停止、WSOLA 音频、
// SPS 级别补丁（服务端）、固定 20ms RTP（服务端）、按设备记忆设置。
import { api } from '../api'

const MIRROR_DEFAULTS = { mode: 'codecs', audioMode: 'both', bitrateMbps: 8, shortSize: 720, maxFps: 60 } as const
export type MirrorSettings = { mode: string; audioMode: string; bitrateMbps: number; shortSize: number; maxFps: number }

const mirrorSettingsKey = (serial: string) => 'ADBreeze-mirror-settings-v3:' + serial

export function hasCodecsSupport() {
  return 'VideoDecoder' in window && 'EncodedVideoChunk' in window
}
export function hasRTCSupport() {
  return 'RTCPeerConnection' in window
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
function savedQualityJSON(serial: string | null) {
  return JSON.stringify(loadMirrorSettings(serial))
}

export interface MirrorStatus {
  title: string
  stats: string
  msg: string
  running: boolean
  muted: boolean
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
let mirrorDecoder: VideoDecoder | null = null
let mirrorSerial: string | null = null
let deviceAudio = false
let audioDup = false
let webrtcMode = false
let webrtcPC: RTCPeerConnection | null = null
let videoW = 720
let videoH = 1280
let waitingKeyframe = false

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

function setStatus(s: Partial<MirrorStatus>) { events?.onStatus(s) }
function showMsg(text: string) { setStatus({ msg: text }) }

export function initController(canvas: HTMLCanvasElement, audio: HTMLAudioElement, ev: Events) {
  canvasEl = canvas
  audioEl = audio
  events = ev
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

function videoCoords(clientX: number, clientY: number) {
  if (!canvasEl) return { x: 0, y: 0 }
  const rect = canvasEl.getBoundingClientRect()
  return {
    x: Math.max(0, Math.min(videoW - 1, Math.round((clientX - rect.left) * videoW / rect.width))),
    y: Math.max(0, Math.min(videoH - 1, Math.round((clientY - rect.top) * videoH / rect.height))),
  }
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
const FIRST_FRAME_TIMEOUT = 12000
let firstFrameTimer: ReturnType<typeof setTimeout> | null = null

function armFirstFrameTimeout() {
  disarmFirstFrameTimeout()
  firstFrameTimer = setTimeout(() => {
    firstFrameTimer = null
    if (waitingKeyframe) {
      waitingKeyframe = false
      setStatus({ phase: 'failed', msg: '画面获取失败：等待首帧超时（设备可能离线或编码器未响应）' })
    }
  }, FIRST_FRAME_TIMEOUT)
}
function disarmFirstFrameTimeout() {
  if (firstFrameTimer) { clearTimeout(firstFrameTimer); firstFrameTimer = null }
}

// 首帧成功：解除等待状态（WebCodecs 在解码输出回调、WebRTC 在 ontrack 里调用）
function firstFrameReady() {
  disarmFirstFrameTimeout()
  if (waitingKeyframe) { waitingKeyframe = false; showMsg('') }
  setStatus({ phase: 'ready' })
}

// ---------- 视频帧统计 ----------
function tickStats() {
  const now = performance.now()
  if (now - lastTick >= 1000) {
    setStatus({
      stats: `${frameCount} fps · ${(byteCount * 8 / 1e6).toFixed(1)} Mbps${audioOn ? ' · 🔊' : ''}`,
    })
    frameCount = 0; byteCount = 0; lastTick = now
  }
}

// ---------- 视频通道（WS 直推 + WebCodecs） ----------
function openVideoWS(serial: string) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const ws = new WebSocket(`${proto}://${location.host}/ws/video/${encodeURIComponent(serial)}?token=${encodeURIComponent(getToken())}`)
  ws.binaryType = 'arraybuffer'
  mirrorWS = ws

  ws.onmessage = ev => {
    if (typeof ev.data === 'string') {
      const msg = JSON.parse(ev.data)
      if (msg.type === 'meta') {
        videoW = msg.width; videoH = msg.height
        lastTitle = `${msg.device}（${msg.width}×${msg.height}）`
        setStatus({ title: lastTitle })
      } else if (msg.type === 'resize') {
        videoW = msg.width; videoH = msg.height
        lastTitle = lastTitle.replace(/（.*）/, `（${msg.width}×${msg.height}）`)
        setStatus({ title: lastTitle })
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

    const isConfig = (flags & 1) !== 0, isKey = (flags & 2) !== 0
    if (isConfig) { configData = payload; return }

    if (!mirrorDecoder || !started) {
      if (!configData) return // 等首个 SPS/PPS 配置包再创建解码器（codec 串由 SPS 推导）
      setupDecoder(configData)
    }
    if (mirrorDecoder!.state !== 'configured') return
    const data = (isKey && configData) ? concat(configData, payload) : payload
    try {
      mirrorDecoder!.decode(new EncodedVideoChunk({ type: isKey ? 'key' : 'delta', timestamp: pts, data }))
    } catch (e) {
      // 还在等首帧时解码失败（如 delta 先于 key 到达）：保持 loading 由超时/后续关键帧兜底，
      // 不解除等待（修复：此前只 showMsg 导致一直转圈永不转失败/成功）
      if (!waitingKeyframe) showMsg('解码异常: ' + (e as Error).message)
    }
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

let started = false
let lastTitle = ''

function setupDecoder(config: Uint8Array | null) {
  if (!canvasEl) return
  let codec = 'avc1.640029'
  const sps = config ? findNAL(config, 7) : null
  if (sps && sps.length >= 4) {
    codec = 'avc1.' + [sps[1], sps[2], sps[3]].map(b => b.toString(16).padStart(2, '0')).join('')
  }
  mirrorDecoder = new VideoDecoder({
    output: frame => {
      firstFrameReady() // 首帧（及后续每帧）到达：解除 loading/超时
      if (canvasEl) {
        if (canvasEl.width !== frame.displayWidth || canvasEl.height !== frame.displayHeight) {
          canvasEl.width = frame.displayWidth
          canvasEl.height = frame.displayHeight
        }
        canvasEl.getContext('2d')!.drawImage(frame, 0, 0)
      }
      frame.close()
    },
    error: e => {
      setStatus({ phase: 'failed', msg: '画面获取失败：解码错误 ' + e.message })
    },
  })
  mirrorDecoder.configure({ codec, optimizeForLatency: true })
  started = true
}

function findNAL(data: Uint8Array, type: number) {
  for (let i = 0; i < data.length - 4; i++) {
    if (data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 0 && data[i + 3] === 1 && (data[i + 4] & 0x1f) === type) {
      let end = data.length
      for (let j = i + 4; j < data.length - 3; j++) {
        if (data[j] === 0 && data[j + 1] === 0 && data[j + 2] === 0 && data[j + 3] === 1) { end = j; break }
      }
      return data.subarray(i + 4, end)
    }
  }
  return null
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
  const ws = new WebSocket(`${proto}://${location.host}/ws/audio/${encodeURIComponent(serial)}?token=${encodeURIComponent(getToken())}`)
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
  const ws = new WebSocket(`${proto}://${location.host}/ws/video/${encodeURIComponent(serial)}?mode=ctrl&token=${encodeURIComponent(getToken())}`)
  mirrorWS = ws
  ws.onmessage = ev => {
    if (typeof ev.data !== 'string') return
    const msg = JSON.parse(ev.data)
    if (msg.type === 'meta') {
      videoW = msg.width; videoH = msg.height
      setStatus({ title: `【WebRTC】${msg.device}（${msg.width}×${msg.height}）` })
    } else if (msg.type === 'resize') {
      videoW = msg.width; videoH = msg.height
      setStatus({ title: lastTitle.replace(/（.*）/, `（${msg.width}×${msg.height}）`) })
    }
  }
  ws.onclose = ev => {
    if (mirrorWS !== ws) return
    mirrorWS = null
    if (mirrorSerial) showMsg(ev.reason || '投屏流已结束')
  }
}

async function startWebrtcViewer(serial: string) {
  if (!hasRTCSupport()) return
  const pc = new RTCPeerConnection()
  webrtcPC = pc
  const a = audioEl!

  pc.ontrack = ev => {
    if (ev.track.kind === 'video') {
      const v = document.createElement('video')
      v.playsInline = true
      v.muted = true
      v.srcObject = ev.streams && ev.streams[0] ? ev.streams[0] : new MediaStream([ev.track])
      v.play().catch(() => {})
      // 隐藏 video 的合成器会丢帧导致 rVFC 不触发，用 rAF 循环 drawImage 取解码帧
      let drewFirst = false
      const drawLoop = () => {
        if (webrtcPC !== pc) return
        if (v.videoWidth && canvasEl) {
          if (canvasEl.width !== v.videoWidth || canvasEl.height !== v.videoHeight) {
            canvasEl.width = v.videoWidth
            canvasEl.height = v.videoHeight
            videoW = v.videoWidth; videoH = v.videoHeight
          }
          canvasEl.getContext('2d')!.drawImage(v, 0, 0, v.videoWidth, v.videoHeight)
          if (!drewFirst) { drewFirst = true; firstFrameReady() } // 首帧真正画上才解除 loading
        }
        requestAnimationFrame(drawLoop)
      }
      requestAnimationFrame(drawLoop)
      // 帧率统计：getStats 的 framesDecoded 增量（隐藏 video 的 totalVideoFrames 不可靠）
      const statsLoop = async () => {
        let prev = 0
        while (webrtcPC === pc) {
          await new Promise(r => setTimeout(r, 1000))
          if (webrtcPC !== pc) return
          const st = await pc.getStats()
          let decoded = 0
          st.forEach(s => { if (s.type === 'inbound-rtp' && s.kind === 'video') decoded = s.framesDecoded })
          if (decoded > prev) clearKeyframeWait()
          if (lastDecoded < 0) { lastDecoded = decoded; prev = decoded; continue }
          setStatus({ stats: `${Math.max(0, decoded - prev)} fps · WebRTC` })
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
    if (webrtcPC === pc && ['failed', 'disconnected'].includes(pc.connectionState) && mirrorSerial) {
      if (waitingKeyframe) {
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

let lastDecoded = -1

// ---------- 启停与切换 ----------
function closeViewers() {
  if (mirrorWS) { mirrorWS.onclose = null; mirrorWS.close(); mirrorWS = null }
  if (mirrorDecoder) { try { mirrorDecoder.close() } catch { /* 已关闭 */ } mirrorDecoder = null }
  closeWebrtcViewer()
  closeAudio()
  started = false
  lastDecoded = -1
}
function closeWebrtcViewer() {
  if (webrtcPC) { try { webrtcPC.close() } catch { /* 已关闭 */ } webrtcPC = null }
  if (audioEl) { audioEl.pause(); audioEl.srcObject = null }
}

export function stopMirror(callApi: boolean) {
  disarmFirstFrameTimeout()
  if (mirrorWS) { mirrorWS.onclose = null; mirrorWS.close(); mirrorWS = null }
  if (mirrorDecoder) { try { mirrorDecoder.close() } catch { /* 已关闭 */ } mirrorDecoder = null }
  closeWebrtcViewer()
  closeAudio()
  if (callApi && mirrorSerial) api('DELETE', `/api/devices/${encodeURIComponent(mirrorSerial)}/mirror`).catch(() => {})
  mirrorSerial = null
  deviceAudio = false
  audioDup = false
  waitingKeyframe = false
  started = false
  if (canvasEl) {
    const ctx = canvasEl.getContext('2d')
    if (ctx) ctx.clearRect(0, 0, canvasEl.width, canvasEl.height)
  }
  setStatus({ running: false, title: '投屏', stats: '', msg: '', phase: 'loading' })
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

export async function startMirror(serial: string) {
  const s = loadMirrorSettings(serial)
  const hasCodecs = hasCodecsSupport()
  const hasRTC = hasRTCSupport()
  if (!hasCodecs && !hasRTC) {
    alert('当前浏览器不支持 WebCodecs 与 WebRTC，无法投屏。请使用 Chrome / Edge 94+ 或较新的 Safari')
    return
  }
  // 一个标签页只投一台：切换设备前先干净结束当前会话（不删服务端流）
  if (mirrorSerial) stopMirror(false)
  mirrorSerial = serial
  deviceAudio = s.audioMode === 'device'
  audioDup = s.audioMode === 'both'
  videoW = 720; videoH = 1280
  let mode = s.mode || 'codecs'
  if (mode === 'codecs' && !hasCodecs) {
    mode = 'webrtc'
    showMsg('当前浏览器不支持 WebCodecs，已自动改用 WebRTC 投屏')
  } else if (mode === 'webrtc' && !hasRTC) {
    mode = 'codecs'
    showMsg('当前浏览器不支持 WebRTC，已自动改用 WebCodecs 投屏')
  }
  webrtcMode = mode === 'webrtc'
  setStatus({ running: true, title: serial + ' — 连接中…', stats: '', phase: 'loading' })
  if (mode === (s.mode || 'codecs')) showMsg('')
  // 清掉上一次的画面：等待新首帧前不能让旧帧造成"已在投"的错觉
  if (canvasEl) {
    const ctx = canvasEl.getContext('2d')
    if (ctx) ctx.clearRect(0, 0, canvasEl.width, canvasEl.height)
  }
  canvasEl?.focus()
  try {
    await api('POST', `/api/devices/${encodeURIComponent(serial)}/mirror`, loadMirrorSettings(serial))
    if (webrtcMode) {
      openCtrlWS(serial)
      await startWebrtcViewer(serial)
    } else {
      openVideoWS(serial)
      if (!deviceAudio) openAudioWS(serial)
    }
    armFirstFrameTimeout() // 启动请求成功但迟迟无首帧 → 超时失败
  } catch (e) {
    setStatus({ phase: 'failed', msg: '画面获取失败：启动投屏失败 ' + (e as Error).message })
  }
}

// 设置应用：仅切方案 → 轻量重建观看通道；画质/音频变更 → 重启设备端流
export async function applySettings(serial: string, next: MirrorSettings) {
  if (!mirrorSerial || mirrorRestarting || mirrorSerial !== serial) return
  const qualityChanged = JSON.stringify(next) !== savedQualityJSON(serial)
  const modeChanged = (next.mode === 'webrtc') !== webrtcMode
  saveMirrorSettings(serial, next)
  if (!qualityChanged && !modeChanged) return
  if (!qualityChanged) {
    mirrorRestarting = true
    // 切换方案：清画面 + loading，等新方案首帧
    if (canvasEl) {
      const ctx = canvasEl.getContext('2d')
      if (ctx) ctx.clearRect(0, 0, canvasEl.width, canvasEl.height)
    }
    waitKeyframe()
    try {
      closeViewers()
      webrtcMode = next.mode === 'webrtc'
      if (webrtcMode) {
        openCtrlWS(serial)
        await startWebrtcViewer(serial)
      } else {
        openVideoWS(serial)
        if (audioDup || !deviceAudio) openAudioWS(serial)
      }
    } catch (e) {
      setStatus({ phase: 'failed', msg: '画面获取失败：切换投屏方案失败 ' + (e as Error).message })
    } finally {
      mirrorRestarting = false
    }
    return
  }
  webrtcMode = next.mode === 'webrtc'
  await restartStream()
}

async function restartStream() {
  const serial = mirrorSerial
  if (!serial || mirrorRestarting) return
  mirrorRestarting = true
  // 参数切换（码率/分辨率/帧率/声音）：清画面 + loading，等新参数流的首帧
  if (canvasEl) {
    const ctx = canvasEl.getContext('2d')
    if (ctx) ctx.clearRect(0, 0, canvasEl.width, canvasEl.height)
  }
  setStatus({ phase: 'loading', msg: '正在应用新参数…' })
  try {
    closeViewers()
    await api('DELETE', `/api/devices/${encodeURIComponent(serial)}/mirror`).catch(() => {})
    const s = loadMirrorSettings(serial)
    await api('POST', `/api/devices/${encodeURIComponent(serial)}/mirror`, {
      audioMode: s.audioMode, bitrateMbps: s.bitrateMbps, shortSize: s.shortSize, maxFps: s.maxFps,
    })
    deviceAudio = s.audioMode === 'device'
    audioDup = s.audioMode === 'both'
    waitKeyframe()
    if (webrtcMode) {
      openCtrlWS(serial)
      await startWebrtcViewer(serial)
    } else {
      openVideoWS(serial)
      if (audioDup || !deviceAudio) openAudioWS(serial)
    }
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

function getToken() {
  return localStorage.getItem('ADBreeze-token') || ''
}
