package com.smanx.adbreeze

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioTrack

/**
 * 静音音频保活：ColorOS（HansManager）/ MIUI 等国产 ROM 会把退后台的进程
 * 冻进 cgroup freezer（实测 logcat: `OplusHansManager: freeze uid ... scene: LcdOn`），
 * 前台服务 + wake lock + 电池白名单都拦不住——冻结器走 ROM 私有路径。
 *
 * 已知唯一可靠的程序化豁免：正在播放音频的应用不冻。于是用 AudioTrack
 * 循环写 8kHz 单声道 16bit 静音采样：
 *   - 零可闻输出（全 0）；缓存 ~0.5s，每次写一小块，CPU 开销可忽略
 *   - 不落 AudioFocus（静音流不该抢焦点，抢了反而会被系统在媒体场景下打断）
 *   - start()/stop() 可重入
 */
object KeepAlive {

    private const val SAMPLE_RATE = 8000
    private const val CHANNEL_MASK = AudioFormat.CHANNEL_OUT_MONO
    private const val ENCODING = AudioFormat.ENCODING_PCM_16BIT

    private var track: AudioTrack? = null
    @Volatile private var running = false
    private var thread: Thread? = null

    fun start() {
        if (running) return
        running = true
        val minBuf = AudioTrack.getMinBufferSize(SAMPLE_RATE, CHANNEL_MASK, ENCODING)
            .coerceAtLeast(SAMPLE_RATE) // ≥0.5s
        val t = AudioTrack.Builder()
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_MEDIA)
                    .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
                    .build()
            )
            .setAudioFormat(AudioFormat.Builder()
                .setSampleRate(SAMPLE_RATE)
                .setEncoding(ENCODING)
                .setChannelMask(CHANNEL_MASK)
                .build())
            .setTransferMode(AudioTrack.MODE_STREAM)
            .setBufferSizeInBytes(minBuf)
            .build()
        track = t
        thread = Thread {
            val silence = ByteArray(2048) // 8kHz*2B ≈ 128ms/块
            try {
                t.play()
                while (running) {
                    // write 阻塞在缓存满时，天然节流；失败即退出等下次 start
                    if (t.write(silence, 0, silence.size) < 0) break
                }
            } catch (_: Exception) {
            } finally {
                runCatching { t.stop() }
                runCatching { t.release() }
            }
        }.apply { name = "adbreeze-keepalive"; start() }
    }

    fun stop() {
        running = false
        thread = null
        track = null // 写线程 finally 里 release
    }
}
