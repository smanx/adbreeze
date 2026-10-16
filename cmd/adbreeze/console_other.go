//go:build !windows

package main

// 非 Windows 平台无需处理控制台代码页（终端默认 UTF-8）。
func enableUTF8Console() {}

func restoreConsoleCP() {}
