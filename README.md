# ADBreeze

**轻风般的跨平台 ADB 控制中心** —— 一个二进制文件，在 Windows / macOS / Linux / Docker / Android 上运行，通过浏览器完成对任意 Android 设备的全部控制：投屏、反向触控、终端、文件管理、剪贴板、截图、APK 安装。

> 本地只跑 ADB，所有功能 UI 全部在 Web 上。

## 特性

- **实时投屏**：基于 scrcpy 协议（H.264），浏览器 WebCodecs 硬解，延迟约 100ms；一路流支持多观众
- **音频转发**：设备声音（Opus）实时转发，浏览器 WebCodecs 解码 + AudioWorklet 播放，可静音
- **反向控制**：鼠标点击/拖动/滚轮、PC 键盘直接操作设备；旋转屏幕自适应（服务端解析 SPS 实时跟踪分辨率）
- **设备剪贴板互通**：读取/写入设备剪贴板，一键复制到本机
- **交互终端**：原生 PTY shell，前端 xterm.js 完整支持 vim/top 等交互程序
- **文件管理**：目录浏览、上传下载、删除、新建（原生 adb sync 协议实现）
- **截图 / APK 安装**：一键 screencap 下载 PNG；浏览器拖入 APK 直接安装
- **批量命令**：勾选多台设备并行执行 shell
- **设备热插拔**：`host:track-devices` 长连接监听，设备上下线实时推送
- **单文件交付**：前端与 scrcpy-server 全部嵌入二进制，部署 = 拷贝一个文件（约 10MB）
- **adb server 自愈**：检测到 adb server 消失时自动拉起

## 架构

```
浏览器 (Web UI)
   │  REST（控制） + WebSocket（视频流 / 终端 / 事件）
   ▼
ADBreeze 服务进程（Go 单二进制）
   ├── ADB 层 ──── adb server（5037，smart socket 协议原生实现）
   ├── scrcpy 层 ── 设备端 scrcpy-server.jar（sync push → 隧道 → client 协议解析）
   └── 设备：USB / adb connect 网络 / 本机自身
```

- **ADB 层**：不依赖 `adb` 子进程（除启动 server 外），原生实现 smart socket（`host:transport` / `host:track-devices` / forward / reverse）与 sync 协议（SEND/DATA/DONE、LIST/RECV）
- **投屏**：嵌入 `scrcpy-server` v2.4，reverse 隧道优先（无连接竞态）、forward 自动回退；视频流经 Hub 扇出给多个 WebSocket 订阅者，新订阅者自动补发 SPS/PPS 并对齐关键帧
- **鉴权**：HTTP Basic Auth（浏览器自带登录弹窗），WebSocket 凭登录后签发的短期会话 cookie

## 快速开始

前端产物 `web/dist` 通过 `go:embed` 嵌入二进制，且不随仓库分发（见 `.gitignore`），因此**克隆后首次构建需先构建前端**（依赖用 [pnpm](https://pnpm.io) 管理）：

```powershell
# 1. 构建前端（Node 18+ / pnpm 9+）
cd web
pnpm install
pnpm run build
cd ..

# 2. 构建（Go 1.22+）
go build -o bin/adbreeze.exe ./cmd/adbreeze

# 3. 运行（密码随机生成并打印在控制台，用户名默认 admin）
.\bin\adbreeze.exe

# 或指定端口与账号密码
.\bin\adbreeze.exe -addr :8000 -user admin -password my-secret
```

浏览器打开 `http://localhost:8000`，在弹出的 Basic Auth 登录框输入账号密码即可使用。

### 一键脚本

项目内的跨平台开发脚本 `scripts/dev.go`（Go 编写，Windows / macOS / Linux 通用）串起「前端构建 → Go 编译 → 启动」全流程：

```bash
go run ./scripts                              # 默认 user=admin, password=admin, addr=:8000
go run ./scripts -user xxx -password yyy -addr :9000
go run ./scripts -skip-web                    # 前端没改时跳过 pnpm build
```

### 开发调试（前端热更新）

改前端代码时不必反复 `pnpm run build` + `go build`。Vite dev server 已配置代理（`/api` 与 `/ws` 转发到 `:8000` 的后端），两个终端分离运行即可：

```bash
# 终端 1：起后端（或直接 go run ./scripts -skip-web）
./bin/adbreeze -user admin -password admin        # Windows 为 .\bin\adbreeze.exe

# 终端 2：起前端开发服务器
cd web
pnpm run dev
```

浏览器打开 `http://localhost:5173`，在 Basic Auth 弹窗输入 `admin` / `admin`。此后修改 `web/src` 下的代码保存即生效（HMR 热更新，投屏会话不中断）；只有改动 Go 代码时才需要重启终端 1 的后端。注意 `pnpm run dev` 与一键脚本的前端构建不要同时进行（都会写 `web/dist`）。

设备接入：

```powershell
adb connect 192.168.1.20:5555   # 网络设备（也可在网页上连接）
# USB 设备插上即出现在列表
```

### Docker

```bash
docker build -t adbreeze .
docker run -p 8000:8000 adbreeze -user admin -password my-secret
# 容器内自带 adb，可 adb connect 管理网络设备
```

### 交叉编译（六平台单文件）

```bash
make build-all   # windows/amd64 arm64 · darwin/amd64 arm64 · linux/amd64 arm64
```

### Android（Termux）

```bash
pkg install android-tools
./adbreeze-linux-arm64          # 运行服务
adb connect 127.0.0.1:5555      # 无线调试自连
```

## API 概览

HTTP 接口通过 Basic Auth 认证（`Authorization: Basic ...`）。WebSocket 不支持 Basic 弹窗，需先 `POST /api/session` 换取短期会话 cookie，握手时由浏览器自动携带。`/api/health` 与 `/api/session` 之外的接口均受保护。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/devices` | 设备列表 |
| POST / DELETE | `/api/devices/connect`、`/api/devices/{serial}` | 网络设备连接 / 断开 |
| POST | `/api/devices/{serial}/shell` | 执行一次性命令 |
| POST / DELETE | `/api/devices/{serial}/mirror` | 投屏启动 / 停止 |
| WS | `/ws/video/{serial}` | 视频流（下行二进制帧，上行 JSON 控制事件） |
| WS | `/ws/term/{serial}` | 交互终端 |
| WS | `/ws/events` | 设备上下线推送 |
| GET/PUT/DELETE | `/api/devices/{serial}/files` | 文件浏览 / 上传 / 删除 |
| GET/POST | `/api/devices/{serial}/clipboard` | 剪贴板读取 / 写入 |
| GET | `/api/devices/{serial}/screenshot` | 截图 PNG |
| POST | `/api/devices/{serial}/install` | APK 安装 |

## 安全提示

ADB 意味着设备的**完全控制权**。密码务必妥善保管，服务默认面向内网使用；公网部署请置于反向代理之后并启用 TLS（Basic Auth 凭证以 Base64 明文编码传输，切勿裸走 HTTP 暴露到公网）。会话 cookie 签名密钥每次启动随机生成，重启服务后所有已登录 WebSocket 会话需重新认证。

## 目录结构

```
adbreeze/
├── cmd/adbreeze/          # 入口
├── internal/
│   ├── adb/               # adb server 协议（smart socket + sync）
│   ├── scrcpy/            # 投屏（server 生命周期 / 协议解析 / ControlMessage / SPS）
│   ├── api/               # REST + WebSocket + 静态前端
│   ├── auth/              # Basic Auth + 会话 cookie
│   └── config/            # 命令行配置
├── web/dist/              # 前端（embed 进二进制）
├── scripts/               # 跨平台开发脚本（go run ./scripts）
├── vendor-bin/            # 内嵌 scrcpy-server jar
├── Dockerfile / Makefile / .github/workflows/release.yml
```

## 致谢

- [scrcpy](https://github.com/Genymobile/scrcpy)（Apache-2.0）—— 投屏核心 `scrcpy-server` 与协议参考
- [ws-scrcpy](https://github.com/DeviceFarmer/ws-scrcpy) —— 协议实现的参考资料

## License

Apache-2.0
