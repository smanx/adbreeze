// Package browser 负责调起系统默认浏览器打开一个 URL。
// 用于桌面端「双击直接运行」的收尾体验：服务就绪后自动把管理界面带到用户面前，
// 省得用户面对一个控制台窗口猜该访问什么地址。
package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// commandFor 返回某平台下打开 URL 使用的命令与参数（纯函数，便于测试）。
func commandFor(goos, url string) (string, []string, error) {
	switch goos {
	case "windows":
		// rundll32 方式不闪黑色命令行窗口（cmd /c start 会闪一下），
		// 参数作为一个整体传给 url.dll 的协议处理器。
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	case "darwin":
		return "open", []string{url}, nil
	case "linux":
		return "xdg-open", []string{url}, nil
	}
	return "", nil, fmt.Errorf("不支持的平台: %s", goos)
}

// Open 用当前平台的默认浏览器打开 url。
// 异步执行（Start 不等待浏览器退出）；调起失败只返回错误，由调用方记日志——
// 自动开浏览器只是便利性兜底，绝不能因此阻断服务本身。
func Open(url string) error {
	name, args, err := commandFor(runtime.GOOS, url)
	if err != nil {
		return err
	}
	return exec.Command(name, args...).Start()
}
