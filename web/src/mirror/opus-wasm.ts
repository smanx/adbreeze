// Opus 软解兜底：WebCodecs 的 AudioDecoder 与 VideoDecoder 一样，
// 只在安全上下文（HTTPS / localhost）暴露。用局域网 IP 以 HTTP 访问时它不存在，
// 音频会直接哑掉——画面有、声音无。这里用 libopus 的 WASM 构建（opus-decoder）
// 补上这一环：不依赖任何安全上下文，输出的 Float32 PCM 直接喂进既有的
// AudioWorklet 播放管线（与 WebCodecs 路径共用，延迟特性一致）。
//
// 动态 import：WASM 仅在真正需要兜底时才下载，不进首屏主包。

import type { OpusDecoderSampleRate } from 'opus-decoder'

/** 模块级单例：多个会话共享同一份已实例化的 wasm 模块 */
let modulePromise: Promise<typeof import('opus-decoder')> | null = null

function loadModule() {
  if (!modulePromise) modulePromise = import('opus-decoder')
  return modulePromise
}

/** OpusHead 里可用的解码参数（多声道需要 channel mapping family） */
export interface OpusParams {
  channels: number
  sampleRate: OpusDecoderSampleRate
  preSkip: number
  streamCount: number
  coupledStreamCount: number
  channelMappingTable: number[]
}

/**
 * 从 OpusHead 配置包解析解码参数。
 * 布局（RFC 7845）：0-7 magic "OpusHead"、8 version、9 channel count、
 * 10-11 pre-skip、12-15 input sample rate、16-17 output gain、18 mapping family；
 * family != 0 时随后是 stream count / coupled count / mapping table。
 */
export function parseOpusHead(head: Uint8Array): OpusParams | null {
  if (head.length < 19) return null
  const channels = head[9]
  if (channels < 1 || channels > 8) return null
  const dv = new DataView(head.buffer, head.byteOffset, head.byteLength)
  const preSkip = dv.getUint16(10, true)
  const family = head[18]
  // scrcpy 采集的 Opus 恒为 48k；libopus 也只接受有限几档输出采样率，固定 48k
  const sampleRate: OpusDecoderSampleRate = 48000
  if (family === 0) {
    return { channels, sampleRate, preSkip, streamCount: 1, coupledStreamCount: channels === 2 ? 1 : 0, channelMappingTable: channels === 2 ? [0, 1] : [0] }
  }
  // family >= 1：映射表长度 = channels
  if (head.length < 21 + channels) return null
  const streamCount = head[19]
  const coupledStreamCount = head[20]
  const channelMappingTable: number[] = []
  for (let i = 0; i < channels; i++) channelMappingTable.push(head[21 + i])
  return { channels, sampleRate, preSkip, streamCount, coupledStreamCount, channelMappingTable }
}

export interface OpusWasmDecoder {
  readonly ready: Promise<void>
  /** 解码一个 Opus 帧，返回按声道分离的 Float32 PCM */
  decodeFrame(frame: Uint8Array): { channelData: Float32Array[]; samplesDecoded: number; sampleRate: number }
  free(): void
}

/**
 * 创建 libopus WASM 解码器。加载失败（网络/内存不足）返回 null，
 * 由调用方决定如何提示；不抛异常以免打断整个投屏会话。
 */
export async function createOpusWasmDecoder(head: Uint8Array): Promise<OpusWasmDecoder | null> {
  const params = parseOpusHead(head)
  if (!params) return null
  try {
    const mod = await loadModule()
    const decoder = new mod.OpusDecoder({
      sampleRate: params.sampleRate,
      channels: params.channels,
      preSkip: params.preSkip,
      streamCount: params.streamCount,
      coupledStreamCount: params.coupledStreamCount,
      channelMappingTable: params.channelMappingTable,
    })
    await decoder.ready
    return decoder as unknown as OpusWasmDecoder
  } catch {
    return null
  }
}
