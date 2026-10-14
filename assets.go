// Package adbreeze 将前端静态资源与 scrcpy-server 内嵌二进制，实现单文件交付。
package adbreeze

import "embed"

//go:embed all:web/dist
var WebFS embed.FS

// ScrcpyServer 是内嵌的 scrcpy-server v5.0.1 jar（原样推送到设备执行）。
// 注意：此 jar 基于 v5.0.1 官方版做过 smali 补丁——移除 AudioPlaybackCapture 中
// voiceCommunicationCaptureAllowed(true) 调用及混音规则里的 USAGE_VOICE_COMMUNICATION(-SIGNALING)
// 两个用例。原因是部分 ROM（ColorOS 13 实测）对含 voice-comm 用例的采集策略要求
// 系统级 CAPTURE_VOICE_COMMUNICATION_OUTPUT 权限，导致 registerAudioPolicy() 返回 -1、
// playback 采集（audio_dup 两边同响）整体不可用；补丁后仅丢失通话音频的采集能力。

//go:embed vendor-bin/scrcpy-server-v5.0.1
var ScrcpyServer []byte

// ScrcpyServerVersion 是内嵌 server 对应的 scrcpy 版本号（协议随版本变化，需配套）。
const ScrcpyServerVersion = "5.0.1"
