// Package adbreeze 将前端静态资源与 scrcpy-server 内嵌二进制，实现单文件交付。
package adbreeze

import "embed"

//go:embed all:web/dist
var WebFS embed.FS

// ScrcpyServer 是内嵌的 scrcpy-server v2.4 jar（原样推送到设备执行）。
//
//go:embed vendor-bin/scrcpy-server-v2.4
var ScrcpyServer []byte

// ScrcpyServerVersion 是内嵌 server 对应的 scrcpy 版本号（协议随版本变化，需配套）。
const ScrcpyServerVersion = "2.4"
