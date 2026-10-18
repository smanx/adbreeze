// Package devlib 是两个开发启动脚本（scripts/agent 与 scripts）的公共实现：
// 前端构建（pnpm）→ Go 编译（go:embed 嵌入 web/dist）→ 启动服务。
// 两个脚本仅默认参数（端口）与使用对象不同，逻辑全部在这里。
package devlib

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Config 描述一个启动脚本的默认参数与标语。
type Config struct {
	// Banner 打印在脚本开头（使用对象说明，见各脚本头部）。
	Banner string
	// DefAddr / DefHTTPSAddr 是该脚本的默认监听地址（两个脚本端口错开）。
	DefAddr     string
	DefHTTPSAdd string
}

// Run 是脚本主流程：解析 flag → 端口占用保护 → 前端构建 → Go 编译 → 启动服务。
func Run(cfg Config) {
	password := flag.String("password", "admin", "管理员密码（前端登录用）")
	addr := flag.String("addr", cfg.DefAddr, "HTTP 监听地址（始终开启）")
	https := flag.Bool("https", true, "额外开启 HTTPS（默认开启；-https=false 关闭）")
	httpsAddr := flag.String("https-addr", cfg.DefHTTPSAdd, "HTTPS 监听地址")
	skipWeb := flag.Bool("skip-web", false, "跳过前端构建（前端没有改动时）")
	flag.Parse()

	fmt.Println(cfg.Banner)

	root, err := projectRoot()
	if err != nil {
		fail(err.Error())
	}

	// 0. 端口占用保护（Windows 下替换运行中的 exe 会损坏文件，且撞端口必然启动失败）。
	// 启用 HTTPS 时两个端口都要查：任一被占用都会让服务起不来。
	toCheck := []string{*addr}
	if *https {
		toCheck = append(toCheck, *httpsAddr)
	}
	for _, a := range toCheck {
		if portInUse(a) {
			fail(fmt.Sprintf("端口 %s 已被占用，请先停止旧进程再运行", a))
		}
	}

	// 1. 前端构建
	if *skipWeb {
		fmt.Println("==> 跳过前端构建 (-skip-web)")
	} else {
		if _, err := exec.LookPath("pnpm"); err != nil {
			fail("未找到 pnpm，请先安装：npm install -g pnpm 或 corepack enable")
		}
		fmt.Println("==> pnpm run build (web)")
		run(filepath.Join(root, "web"), "pnpm", "run", "build")
	}

	// 2. 后端编译（go:embed 嵌入 web/dist）
	exeName := "adbreeze"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	exePath := filepath.Join(root, "bin", exeName)
	if err := os.MkdirAll(filepath.Dir(exePath), 0o755); err != nil {
		fail("创建 bin 目录失败: " + err.Error())
	}
	fmt.Println("==> go build (embed web/dist)")
	run(root, "go", "build", "-ldflags", "-s -w -X main.version=dev", "-o", exePath, "./cmd/adbreeze")

	// 3. 启动（前台运行，Ctrl+C 退出）。-https 与 -https-addr 始终显式传递：
	// 后端同为布尔 flag，不能靠省略表达关闭（省略 = 后端默认值 true）
	args := []string{"-addr", *addr, "-password", *password,
		"-https=" + fmt.Sprintf("%v", *https), "-https-addr", *httpsAddr}
	if *https {
		fmt.Printf("==> 启动 http://localhost%s 与 https://localhost%s (管理员密码: %s)\n",
			*addr, *httpsAddr, *password)
	} else {
		fmt.Printf("==> 启动 http://localhost%s (管理员密码: %s)\n", *addr, *password)
	}
	run(root, exePath, args...)
}

// projectRoot 定位项目根目录：从当前目录向上找 go.mod（且要求同级存在 web/ 与 cmd/），
// 这样在 web/ 等子目录里执行 go run ../scripts 也能正确定位。
func projectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if has(filepath.Join(dir, "go.mod")) && has(filepath.Join(dir, "web")) && has(filepath.Join(dir, "cmd")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到项目根目录（需在 adbreeze 仓库内运行）")
		}
		dir = parent
	}
}

// portInUse 探测 addr 对应端口是否已有监听（连接成功即视为占用）。
// 比 Listen 试探可靠：Windows 上占用方的独占绑定会让 Listen 报出不同措辞的错误，无法按文案区分。
func portInUse(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func has(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// run 流式执行子进程并透传 stdio；非零退出即终止脚本。
func run(dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fail(fmt.Sprintf("%s %v 失败: %v", name, args, err))
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "错误: "+msg)
	os.Exit(1)
}
