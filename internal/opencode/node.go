package opencode

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// DetectNode 检测 Node.js / npx 是否可用。返回 node 版本首行与 npx 路径。
// mobile-mcp 是 npx 包，依赖 Node.js 20+。
func DetectNode() (nodeVersion, npxPath string) {
	npx, err := exec.LookPath("npx")
	if err == nil {
		npxPath = npx
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "node", "--version").CombinedOutput(); err == nil {
		nodeVersion = strings.TrimSpace(string(out))
	}
	return nodeVersion, npxPath
}

// NodeMajor 从 "v20.11.0" 解析出主版本号，失败返回 0。
func NodeMajor(v string) int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	n, _ := strconv.Atoi(v)
	return n
}
