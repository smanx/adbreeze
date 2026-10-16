// WASM 软解兜底：移植自 ya-webadb（Tango）的 scrcpy-decoder-h264bsd。
//
// 价值：WebCodecs 只在安全上下文（HTTPS / localhost）暴露，HTTP+IP 访问时
// Chrome/Edge/Android 直接不提供 VideoDecoder；此前这种情况只能改用 WebRTC
// （需要 ICE/NAT 可通）。h264bsd（Android 开源 H.264 解码器的 Emscripten 构建）
// 不依赖任何安全上下文，给了一条纯前端的兜底路。
//
// 与 Tango 的差异：
//   * Tango 用 yuv-canvas 上屏；这里接到本项目 FrameRenderer 的 YUV 纹理路径，
//     少一次 RGBA 转换与拷贝。
//   * 动态 import：h264bsd 的 wasm（约 119KB）不进首屏主包，只有真正用到兜底
//     或用户显式选择软解时才下载。

import type { FrameRenderer } from './render'
import { t } from '../i18n'

/** 模块级单例：多个会话共享同一份已实例化的 wasm 模块 */
let modulePromise: Promise<H264BsdModule> | null = null

interface PictureInfo {
  picWidth: number
  picHeight: number
  videoRange: number
  matrixCoefficients: number
  croppingFlag: number
  cropParams: { cropLeftOffset: number; cropOutWidth: number; cropTopOffset: number; cropOutHeight: number }
}
interface Picture {
  info: PictureInfo
  bytes: Uint8Array
  picId: number
  isIdr: boolean
  nbrOfErrMBs: number
}
interface DecodeResult { code: number; read: number; picture?: Picture; extraPictureCount: number }
interface FlushResult { picture?: Picture; extraPictureCount: number }
interface H264BsdDecoder {
  decode(data: Uint8Array, picId: number): DecodeResult
  getNextPicture(): Picture | undefined
  flush(): FlushResult
  setIntraConcealmentMethod(method: number): void
  delete(): void
}
interface H264BsdModule {
  DecodeResultCode: { Ready: number; PictureReady: number; HeadersReady: number; Error: number; ParamSetError: number; MemoryError: number }
  Decoder: new () => H264BsdDecoder
}

async function loadModule(): Promise<H264BsdModule> {
  if (!modulePromise) {
    modulePromise = import('@yume-chan/h264bsd').then(m => m.default() as unknown as H264BsdModule)
  }
  return modulePromise
}

/** BT.709 矩阵族（matrix_coefficients 1/9/10）；其余（含未指定 2）按 BT.601 */
function isBT709(mc: number) {
  return mc === 1 || mc === 9 || mc === 10
}

/**
 * AnnexB H.264 → h264bsd 软解 → FrameRenderer YUV 上屏。
 *
 * 能力边界（来自上游 h264bsd 的官方声明）：仅支持 Baseline profile、
 * Level ≤ 4。Android 设备默认多用 High profile，此时解码会返回错误——
 * 这不是本封装的缺陷，而是该解码器的固有限制，调用方应据此降级到其他方案。
 * 解码同步发生在主线程（720p30 约 5~15ms/帧）。
 */
export class SoftH264Decoder {
  private decoder: H264BsdDecoder | null = null
  private module: H264BsdModule | null = null
  /** 逐帧取渲染器：画布被 React 换掉时渲染器会重建，持实例引用会画到废弃画布上 */
  private getRenderer: () => FrameRenderer | null
  private onError: (message: string) => void
  private onStats?: () => void
  /** 每输出一幅调用（首帧判定用：软解没有 VideoFrame 回调，需显式通知） */
  private onPicture?: () => void
  /** 帧序号（h264bsd 的 picId，用于错误隐藏关联） */
  private picId = 0
  /** 输出帧回调：解码成功一帧后调用（用于首帧判定与统计） */
  private framesDecoded = 0
  private errorCount = 0
  private disposed = false
  /**
   * 必须从 IDR 开始解码：h264bsd 收到无参考的 delta 帧会直接返回 Error。
   * 初值为 true —— wasm 加载期间到达的帧多为 delta，若不禁用会以错误开场。
   * 每次出错后重新置 true，等下一个关键帧（GOP=2s）再同步。
   */
  private needsKeyframe = true
  /**
   * 连续在 IDR 上失败：说明码流本身超出解码器能力（多为 High profile），
   * 重试无意义，通知调用方降级到其他方案。
   */
  private onUnsupported?: () => void
  private keyErrors = 0

  constructor(
    getRenderer: () => FrameRenderer | null,
    onError: (message: string) => void,
    opts?: { onStats?: () => void; onPicture?: () => void; onUnsupported?: () => void },
  ) {
    this.getRenderer = getRenderer
    this.onError = onError
    this.onStats = opts?.onStats
    this.onPicture = opts?.onPicture
    this.onUnsupported = opts?.onUnsupported
  }

  get ready() { return !!this.decoder }
  get counters() { return { framesDecoded: this.framesDecoded, errors: this.errorCount } }

  /** 预热：下载并实例化 wasm（失败不致命，push 时会再试） */
  async init() {
    if (this.decoder || this.disposed) return
    try {
      const mod = await loadModule()
      if (this.disposed) return
      this.module = mod
      this.decoder = new mod.Decoder()
    } catch (e) {
      this.onError(t('soft.loadFailed') + (e as Error).message)
    }
  }

  /** 配置包（SPS/PPS）无需特殊处理：h264bsd 从码流自身解析 */
  setConfiguration() { /* no-op */ }

  /** 送入一帧（AnnexB 裸流，关键帧需自带 SPS/PPS） */
  push(data: Uint8Array, isKey: boolean) {
    const mod = this.module
    const dec = this.decoder
    if (!mod || !dec || this.disposed) return
    if (this.needsKeyframe && !isKey) return
    if (isKey) this.needsKeyframe = false

    const errCode = mod.DecodeResultCode.Error
    let offset = 0
    let length = data.length
    let last: Picture | undefined
    let produced = 0

    try {
      while (length > 0) {
        const result = dec.decode(data.subarray(offset), this.picId)
        if (result.code >= errCode) {
          this.errorCount++
          this.needsKeyframe = true
          // 在 IDR 起始处失败 = 码流本身不支持（profile/level 超出能力），
          // 连续两次即判定不可用；在 P 帧中间失败则只是丢包，继续等下个 IDR
          if (isKey) this.keyErrors++
          if (this.keyErrors >= 2) {
            this.onError(t('soft.tooWeak'))
            this.onUnsupported?.()
          } else {
            this.onError(t('soft.failed', { code: result.code }))
          }
          return
        }
        const pic = this.lastPicture(dec, result)
        if (pic) { produced++; last = pic }
        offset += result.read
        length -= result.read
        if (result.read === 0) break // 防御：避免零长度推进死循环
      }
    } catch (e) {
      this.errorCount++
      this.needsKeyframe = true
      this.onError(t('soft.error') + (e as Error).message)
      return
    }

    this.picId++
    if (last) this.keyErrors = 0 // 成功出图即清零，避免偶发错误误判为不支持
    if (last) {
      this.framesDecoded += produced
      // 只渲染最新一幅（h264bsd 输出顺序即显示顺序，无重排缓冲）
      this.draw(last)
      this.onPicture?.()
      this.onStats?.()
    }
  }

  /** 取本次调用产出的最后一幅（extraPictureCount 表示还有多少待发） */
  private lastPicture(dec: H264BsdDecoder, result: FlushResult): Picture | undefined {
    let pic = result.picture
    if (pic) {
      for (let i = 0; i < result.extraPictureCount; i++) pic = dec.getNextPicture() ?? pic
      return pic
    }
    return undefined
  }

  private draw(p: Picture) {
    const info = p.info
    const w = info.picWidth
    const h = info.picHeight
    if (!w || !h) return
    const cw = Math.max(1, w >> 1)
    const uOffset = w * h
    const vOffset = uOffset + cw * Math.max(1, h >> 1)
    const cp = info.cropParams
    const cropW = info.croppingFlag ? cp.cropOutWidth : w
    const cropH = info.croppingFlag ? cp.cropOutHeight : h
    const renderer = this.getRenderer()
    if (!renderer) return
    renderer.renderYUV({
      y: p.bytes.subarray(0, uOffset),
      u: p.bytes.subarray(uOffset, vOffset),
      v: p.bytes.subarray(vOffset, vOffset + cw * Math.max(1, h >> 1)),
      stride: w,
      chromaStride: cw,
      lumaWidth: w,
      lumaHeight: h,
      cropLeft: info.croppingFlag ? cp.cropLeftOffset : 0,
      cropTop: info.croppingFlag ? cp.cropTopOffset : 0,
      cropWidth: cropW || w,
      cropHeight: cropH || h,
      // video_range 标志：0 = 有限范围（Android 默认），1 = 全范围
      limitedRange: info.videoRange !== 1,
      bt709: isBT709(info.matrixCoefficients),
    })
  }

  dispose() {
    this.disposed = true
    try { this.decoder?.delete() } catch { /* 已释放 */ }
    this.decoder = null
  }
}

/** 供 UI 判断是否值得暴露软解选项 */
export function hasWebAssembly() {
  return typeof WebAssembly !== 'undefined'
}
