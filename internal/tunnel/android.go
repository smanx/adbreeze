package tunnel

import (
	"context"
	"os"
	"os/exec"
	"runtime"
)

// android 上无法直接 exec 应用私有目录里的二进制：targetSdk 29+ 的 SELinux
// 策略禁止 untrusted_app 对 app_data_file 的 execute。解法与 APK 壳的
// ExecUtil 同款——经系统动态加载器包装执行：
//   - arm64 真机：/system/bin/linker64（首选 ABI 即 arm64）；
//   - x86_64 模拟器（NativeBridge 转译）：/system/bin/arm64/linker64。
//
// linker 路径由 APK 壳经环境变量 ADBREEZE_LINKER 注入（ExecUtil.pickLinker()）；
// Go 服务在 PC 上独立运行时（非 android）恒走直启。
//
// 注意：只能跑「GOOS=android 的动态 PIE」（bionic 链接），静态 linux ELF
// 会被 linker64 拒绝（e_type != ET_DYN）——这正是 android 端不用官方
// linux/arm64 二进制而改用 Termux 仓库构建的原因（见 install_android.go）。
func cloudflaredCmd(ctx context.Context, bin string, args ...string) *exec.Cmd {
	if runtime.GOOS != "android" {
		return exec.CommandContext(ctx, bin, args...)
	}
	linker := os.Getenv("ADBREEZE_LINKER")
	if linker == "" {
		linker = "/system/bin/linker64"
	}
	argv := append([]string{bin}, args...)
	return exec.CommandContext(ctx, linker, argv...)
}
