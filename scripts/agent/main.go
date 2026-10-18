package main

// AI Agent 专用开发启动脚本（用户请勿使用本脚本，用户用 go run ./scripts）：
// 前端构建（pnpm）→ Go 编译（go:embed 嵌入 web/dist）→ 启动服务。
//
// ⚠️ 使用对象划分：
//   - 本脚本（go run ./scripts/agent）：专为 AI Agent / 自动化工具准备；
//   - 用户手动启动请用 go run ./scripts（端口 9080/9443）。
//
// 与用户脚本唯一的区别是默认端口（9081/9444 而非 9080/9443）：
// 两者可同时运行互不干扰，Agent 的服务不会顶掉用户正在用的实例。
//
// 用法（项目根目录）：
//
//	go run ./scripts/agent                       # 默认密码 admin，http://localhost:9081 + https://localhost:9444
//	go run ./scripts/agent -password yyy
//	go run ./scripts/agent -skip-web             # 前端没改时跳过 pnpm build
//	go run ./scripts/agent -https=false          # 关闭 HTTPS（HTTP 始终可用）
//	go run ./scripts/agent -addr :9099           # 需要时也可显式覆盖端口
//
// 脚本以裸 main 包存在，不参与 go build ./cmd/... 的产物构建。

import "adbreeze/scripts/devlib"

func main() {
	devlib.Run(devlib.Config{
		Banner: "【AI Agent 专用启动脚本】与用户脚本（go run ./scripts）端口错开，可并行运行",
		// 默认端口：Agent 专用 9081/9444（避开用户脚本的 9080/9443）
		DefAddr:     ":9081",
		DefHTTPSAdd: ":9444",
	})
}
