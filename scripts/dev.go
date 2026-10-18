package main

// 用户手动使用的开发启动脚本（AI Agent 请勿使用本脚本！）：
// 前端构建（pnpm）→ Go 编译（go:embed 嵌入 web/dist）→ 启动服务。
//
// ⚠️ 使用对象划分：
//   - 本脚本（go run ./scripts）：仅供用户手动使用；
//   - AI Agent / 自动化工具请使用 go run ./scripts/agent（端口错开，见该脚本头部说明）。
//
// 用法（项目根目录）：
//
//	go run ./scripts                        # 默认密码 admin，http://localhost:9080 + https://localhost:9443
//	go run ./scripts -password yyy
//	go run ./scripts -skip-web              # 前端没改时跳过 pnpm build
//	go run ./scripts -https=false           # 关闭 HTTPS（HTTP 始终可用）
//
// 脚本以裸 main 包存在，不参与 go build ./cmd/... 的产物构建。

import "adbreeze/scripts/devlib"

func main() {
	devlib.Run(devlib.Config{
		Banner: "【用户手动启动脚本】AI Agent 请改用：go run ./scripts/agent（端口 9081/9444）",
		// 默认端口：用户脚本占 9080/9443（与后端默认值一致）
		DefAddr:     ":9080",
		DefHTTPSAdd: ":9443",
	})
}
