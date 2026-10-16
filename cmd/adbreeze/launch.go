package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"time"

	"adbreeze/internal/browser"
	"adbreeze/internal/config"
)

// fatal 打印启动失败原因并退出进程。
// 双击启动（Windows 资源管理器 / macOS Finder，无命令行参数）时，控制台窗口
// 随进程退出而立即关闭，错误信息一闪而过；先等用户按回车再退。
// 带参数启动（控制台 / 服务）不等待，避免卡住脚本与 systemd。
func fatal(format string, args ...any) {
	log.Printf(format, args...)
	if waitBeforeExit() {
		fmt.Print("\n启动失败，按回车键退出...")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	restoreConsoleCP()
	os.Exit(1)
}

// waitBeforeExit 判断退出前是否等待用户按键。
// 判据是「无命令行参数启动」：双击 / Finder 场景的特征；
// 控制台里直接敲 adbreeze.exe 同样无参数，此时等一下也无妨（错误罕见，
// 读一眼信息比窗口秒关好）。Linux 一律不等（服务器 / Docker / Termux）。
func waitBeforeExit() bool {
	switch runtime.GOOS {
	case "windows":
		return len(os.Args) <= 1
	case "darwin":
		// Finder 双击启动不经过终端，TERM_PROGRAM 不会设置；
		// 终端里启动会带上它，不阻塞控制台用户。
		return len(os.Args) <= 1 && os.Getenv("TERM_PROGRAM") == ""
	}
	return false
}

// maybeOpenBrowser 服务就绪后按需调起默认浏览器（异步，不阻塞启动流程）。
// 仅便利性兜底：失败只记日志，人工访问启动日志里打印的地址即可。
func maybeOpenBrowser(cfg *config.Config) {
	if !cfg.OpenBrowser {
		return
	}
	hp := hostPort(cfg.Addr)
	url := "http://" + hp
	go func() {
		if !waitPort(hp, 3*time.Second) {
			log.Printf("[browser] HTTP 服务未就绪，跳过自动打开浏览器（可手动访问 %s）", url)
			return
		}
		if err := browser.Open(url); err != nil {
			log.Printf("[browser] 调起默认浏览器失败（可手动访问 %s）: %v", url, err)
			return
		}
		log.Printf("[browser] 已调起默认浏览器: %s", url)
	}()
}

// hostPort 把监听地址换算成浏览器可用的 host:port：
// 监听在全部网卡（"" / 0.0.0.0 / ::）时，本机浏览器应访问 localhost。
func hostPort(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

// waitPort 等待 TCP 端口可连接：HTTP 服务在 goroutine 里 ListenAndServe，
// 「就绪」日志打印时端口可能还没 bind 完，自动开浏览器前先确认真的能连上。
func waitPort(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
