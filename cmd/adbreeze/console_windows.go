//go:build windows

package main

import (
	"log"
	"syscall"

	"golang.org/x/sys/windows"
)

// consoleCPUTF8 是 UTF-8 代码页编号（x/sys/windows 未导出该常量）。
const consoleCPUTF8 = 65001

// consoleCP 记录进入时的控制台输出代码页，退出时恢复：
// 从 cmd 启动的场景下，改的是用户自己那个控制台，退出必须还回去，
// 否则用户后续在同一个 cmd 窗口里的中文输出会变乱码。
var consoleCP uint32

// enableUTF8Console 把控制台输出代码页切到 UTF-8。
// Go 写出的日志是 UTF-8 字节流，而中文 Windows 控制台默认 GBK(936)，
// 双击启动时中文日志全是乱码。只在 stdout 真的挂在控制台上时才处理——
// 重定向到文件 / 管道时 GetConsoleMode 失败，无需也无法处理。
func enableUTF8Console() {
	var mode uint32
	if err := syscall.GetConsoleMode(syscall.Stdout, &mode); err != nil {
		return // 不是控制台（重定向 / 服务场景）
	}
	cp, err := windows.GetConsoleOutputCP()
	if err != nil || cp == consoleCPUTF8 {
		return
	}
	if err := windows.SetConsoleOutputCP(consoleCPUTF8); err != nil {
		log.Printf("[console] 切换控制台代码页到 UTF-8 失败（日志可能显示乱码）: %v", err)
		return
	}
	consoleCP = cp
}

// restoreConsoleCP 退出前把控制台代码页还回去（仅在本进程切过时才动）。
func restoreConsoleCP() {
	if consoleCP == 0 {
		return
	}
	_ = windows.SetConsoleOutputCP(consoleCP)
	consoleCP = 0
}
