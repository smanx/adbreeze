// WebCodecs 视频解码封装：移植自 ya-webadb（Tango）的 VideoDecoderStream。
//
// 相比此前「一个 VideoDecoder + 出错即转失败」的写法，吸收四点能力：
//   1. 关键帧以来的码流缓冲：解码器需要重建时从最近一个关键帧重放，画面
//      立即接上最新帧，而不是干等下一个 GOP（本项目 i-frame-interval=2，
//      静止画面下等待可能更久）。
//   2. Chrome 的「闲置解码器回收」（QuotaExceededError，约 90 秒无活动，
//      常见于后台标签页暂停期间）：不当错误处理，转 reclaimed 状态，
//      下一帧到来时自动重建。
//   3. 硬解器自身故障（EncodingError）：先探测软件解码可用，重建为
//      prefer-software 并重放缓冲，重放期间丢弃旧时间戳的输出帧；
//      仍失败才上报失败。
//   4. 统计埋点：framesDecoded / framesSkippedDecoding / decoderResets /
//      decodeQueueSize / 实际软硬解模式，供状态栏与自适应丢帧使用。
import { t } from '../i18n'

/** 送入解码器的一帧（已按 scrcpy 规则拼好 SPS/PPS） */
export interface RawChunk {
  key: boolean
  /** 微秒（scrcpy PTS 单位） */
  timestamp: number
  duration: number
  data: Uint8Array
}

export interface DecoderCounters {
  framesDecoded: number
  framesSkippedDecoding: number
  decoderResets: number
  /** 因解码积压被主动跳过的输入帧数（追帧） */
  framesDroppedBackpressure: number
}

type State =
  | { type: 'unconfigured' }
  | { type: 'configured'; decoder: VideoDecoder; config: VideoDecoderConfig }
  | { type: 'decoding'; decoder: VideoDecoder; config: VideoDecoderConfig }
  | { type: 'reclaimed'; config: VideoDecoderConfig }

export interface H264DecoderOptions {
  /** 输出帧回调：VideoFrame 所有权移交调用方（必须 close） */
  onFrame: (frame: VideoFrame) => void
  onError: (message: string) => void
  onStats?: () => void
  /** 队列深度超过该值时丢帧追帧（0 = 关闭自适应追帧） */
  maxQueue?: number
}

/** 从 SPS 推导 codec 串（沿用既有实现：profile/compat/level 三个字节） */
export function codecFromSPS(sps: Uint8Array | null): string {
  if (sps && sps.length >= 4) {
    return 'avc1.' + [sps[1], sps[2], sps[3]].map(b => b.toString(16).padStart(2, '0')).join('')
  }
  return 'avc1.640029'
}

/**
 * 读取 AnnexB 配置包中 SPS 的 profile_idc（NAL payload 第 2 字节）。
 * 66 = Baseline / Constrained Baseline（h264bsd 唯一支持的档位）；
 * 100 = High（多数 Android 机型默认）。无 SPS 时返回 null。
 */
export function spsProfileIdc(annexb: Uint8Array): number | null {
  const sps = findNALU(annexb, 7)
  if (!sps || sps.length < 2) return null
  return sps[1]
}

/** 在 AnnexB 码流中定位指定类型 NAL 的载荷（不含起始码） */
export function findNALU(data: Uint8Array, type: number): Uint8Array | null {
  for (let i = 0; i + 4 < data.length; i++) {
    if (data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 0 && data[i + 3] === 1 && (data[i + 4] & 0x1f) === type) {
      let end = data.length
      for (let j = i + 4; j + 3 < data.length; j++) {
        if (data[j] === 0 && data[j + 1] === 0 && data[j + 2] === 0 && data[j + 3] === 1) { end = j; break }
      }
      return data.subarray(i + 4, end)
    }
  }
  return null
}

const MAX_BUFFERED = 300 // 重放缓冲上限（帧）：约 5 秒 @60fps，超出只保留关键帧之后

/**
 * H.264 AnnexB → WebCodecs 解码器封装。
 * 用法：setConfiguration(spsPps) 一次或多次，随后 push(chunk)。
 */
export class H264Decoder {
  private state: State = { type: 'unconfigured' }
  private config: VideoDecoderConfig | null = null
  private opts: H264DecoderOptions
  private buffered: RawChunk[] = []
  private skipBefore = -1
  private closed = false
  /** 实际生效的硬件加速策略（重放降级后为 prefer-software） */
  private hwMode: VideoDecoderConfig['hardwareAcceleration'] = 'no-preference'

  counters: DecoderCounters = { framesDecoded: 0, framesSkippedDecoding: 0, decoderResets: 0, framesDroppedBackpressure: 0 }

  constructor(opts: H264DecoderOptions) {
    this.opts = opts
  }

  get decodeQueueSize() {
    return 'decoder' in this.state ? this.state.decoder.decodeQueueSize : 0
  }
  get hardwareAcceleration() { return this.hwMode }
  get started() { return this.state.type === 'decoding' }

  /** 收到 SPS/PPS 配置包：记录 codec 串；已有解码器则原地 reconfigure */
  setConfiguration(annexb: Uint8Array) {
    const sps = findNALU(annexb, 7)
    // AnnexB 内联参数集：不传 description，SPS/PPS 随关键帧一起送入
    const next: VideoDecoderConfig = {
      codec: codecFromSPS(sps),
      optimizeForLatency: true,
      hardwareAcceleration: this.hwMode,
    }
    if (this.config?.codec === next.codec && ('decoder' in this.state) && this.state.decoder.state === 'configured') {
      // 同一 codec（旋转常见）：复用解码器实例，只重新 configure
      const decoder = this.state.decoder
      decoder.configure(next)
      this.state = { type: 'configured', decoder, config: next }
      this.buffered.length = 0
      this.config = next
      return
    }
    if ('decoder' in this.state && this.state.decoder.state !== 'closed') {
      try { this.state.decoder.close() } catch { /* 已关闭 */ }
    }
    this.config = next
    this.state = { type: 'unconfigured' }
    this.buffered.length = 0
  }

  push(chunk: RawChunk) {
    if (this.closed || !this.config) return
    const maxQueue = this.opts.maxQueue ?? 0
    // 自适应追帧：积压过深时丢 delta 直到下一个关键帧（WebCodecs 无法从
    // delta 中间恢复，故只能整段丢到 GOP 边界）
    if (maxQueue > 0 && !chunk.key && this.decodeQueueSize > maxQueue) {
      this.counters.framesDroppedBackpressure++
      this.opts.onStats?.()
      return
    }

    if (chunk.key) this.buffered.length = 0
    this.remember(chunk)

    if (!chunk.key && this.state.type !== 'decoding') return // delta 无参考帧，丢弃

    switch (this.state.type) {
      case 'unconfigured': {
        const decoder = this.createDecoder(this.config)
        this.state = { type: 'decoding', decoder, config: this.config }
        this.rawDecode(decoder, chunk)
        break
      }
      case 'configured': {
        const decoder = this.state.decoder
        this.state = { type: 'decoding', decoder, config: this.config }
        this.rawDecode(decoder, chunk)
        break
      }
      case 'decoding':
        this.rawDecode(this.state.decoder, chunk)
        break
      case 'reclaimed': {
        // 浏览器回收了硬解资源：重建并重放关键帧以来的缓冲，立即追平画面
        const decoder = this.createDecoder(this.config)
        this.state = { type: 'decoding', decoder, config: this.config }
        this.counters.decoderResets++
        this.skipBefore = chunk.timestamp
        const firstKey = this.buffered.findIndex(f => f.key)
        if (firstKey >= 0) for (const f of this.buffered.slice(firstKey)) this.rawDecode(decoder, f)
        break
      }
    }
    this.opts.onStats?.()
  }

  private remember(chunk: RawChunk) {
    this.buffered.push(chunk)
    if (this.buffered.length > MAX_BUFFERED) {
      // 溢出：丢弃最旧，但必须从某个关键帧之后开始，保证重放可用
      const excess = this.buffered.length - MAX_BUFFERED
      this.buffered.splice(0, excess)
    }
  }

  private rawDecode(decoder: VideoDecoder, chunk: RawChunk) {
    if (decoder.state !== 'configured') return
    try {
      decoder.decode(new EncodedVideoChunk({
        type: chunk.key ? 'key' : 'delta',
        timestamp: chunk.timestamp,
        duration: chunk.duration,
        data: chunk.data,
      }))
    } catch (e) {
      // 解码器被浏览器回收后可能已 closed：转为 reclaimed 等下一帧重建
      if ((e as Error).name === 'InvalidStateError') {
        if (this.state.type === 'decoding' || this.state.type === 'configured') {
          this.state = { type: 'reclaimed', config: this.config! }
        }
        return
      }
      this.opts.onError(t('dec.dispatchFailed') + (e as Error).message)
    }
  }

  private createDecoder(config: VideoDecoderConfig): VideoDecoder {
    const decoder = new VideoDecoder({
      output: frame => {
        if (this.closed) { frame.close(); return }
        if (frame.timestamp < this.skipBefore) {
          // 重放产生的旧帧：不计入解码成功数，单独统计为跳过
          frame.close()
          this.counters.framesSkippedDecoding++
          this.opts.onStats?.()
          return
        }
        this.counters.framesDecoded++
        this.opts.onFrame(frame)
        this.opts.onStats?.()
      },
      error: e => this.handleError(e, decoder),
    })
    decoder.configure(config)
    return decoder
  }

  private handleError(e: DOMException | Error, decoder: VideoDecoder) {
    if (this.closed) return
    // 浏览器主动回收闲置硬解器（Chrome ~90s）：保留缓冲，下次 push 重建
    if (e.name === 'QuotaExceededError') {
      if ('decoder' in this.state && this.state.decoder === decoder) {
        this.state = { type: 'reclaimed', config: this.config! }
      }
      this.opts.onStats?.()
      return
    }
    // 硬解器内部故障：尝试降级为软件解码重放
    if (e.name === 'EncodingError' && this.hwMode !== 'prefer-software' && this.config) {
      const swConfig: VideoDecoderConfig = { ...this.config, hardwareAcceleration: 'prefer-software' }
      const pending = this.buffered
      void VideoDecoder.isConfigSupported(swConfig).then(support => {
        if (!support.supported || this.closed) {
          this.opts.onError(t('dec.error', { msg: e.message }))
          return
        }
        this.hwMode = 'prefer-software'
        this.counters.decoderResets++
        const last = pending[pending.length - 1]
        const d = this.createDecoder(swConfig)
        this.state = { type: 'decoding', decoder: d, config: swConfig }
        const firstKey = pending.findIndex(f => f.key)
        if (firstKey >= 0 && last) {
          this.skipBefore = last.timestamp
          for (const f of pending.slice(firstKey)) this.rawDecode(d, f)
        }
        this.opts.onStats?.()
      }).catch(() => this.opts.onError(t('dec.error', { msg: e.message })))
      return
    }
    this.opts.onError(t('dec.error', { msg: e.message }))
  }

  close() {
    this.closed = true
    if ('decoder' in this.state && this.state.decoder.state !== 'closed') {
      try { this.state.decoder.close() } catch { /* 已关闭 */ }
    }
    this.state = { type: 'unconfigured' }
    this.buffered.length = 0
  }
}
