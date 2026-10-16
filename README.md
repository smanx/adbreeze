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

# 3. 运行：所有参数都可省略，默认 admin/admin，HTTP :9080 + HTTPS :9443
.\bin\adbreeze.exe

# 需要时用参数覆盖（优先级：命令行 > 环境变量 > 默认值）
.\bin\adbreeze.exe -addr :8000 -https=false -user admin -password my-secret
```

浏览器打开 `http://localhost:9080`（HTTPS 在 `https://localhost:9443`），在弹出的 Basic Auth 登录框输入账号密码即可使用。

### 配置（命令行 / 环境变量 / 默认值）

三项来源优先级从高到低：**命令行参数 > 环境变量 > 默认值**。全部可省略。

| 配置 | 命令行 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|---|
| 登录用户名 | `-user` | `ADBREEZE_USER` | `admin` | Basic Auth 用户名 |
| 登录密码 | `-password` | `ADBREEZE_PASSWORD` | `admin` | Basic Auth 密码 |
| HTTP 端口 | `-addr` | `ADBREEZE_ADDR` | `:9080` | HTTP 始终开启；只写端口号（如 `9080`）也可以 |
| HTTPS 开关 | `-https` | `ADBREEZE_HTTPS` | `true` | `-https=false` 关闭（HTTP 始终可用）|
| HTTPS 端口 | `-https-addr` | `ADBREEZE_HTTPS_ADDR` | `:9443` | 与 HTTP 必须不同端口 |
| adb 路径 | `-adb` | `ADBREEZE_ADB` | 从 PATH 查找 | 也可在网页设置里随时切换 |
| 数据目录 | `-data` | `ADBREEZE_DATA` | 用户配置目录下的 `ADBreeze` | 存放 TLS 证书，需持久保存 |
| TLS 证书 | `-tls-cert` / `-tls-key` | `ADBREEZE_TLS_CERT` / `ADBREEZE_TLS_KEY` | 自动生成 | 指定正式证书时成对使用 |
| 追加证书主机名 | `-tls-hosts` | `ADBREEZE_TLS_HOSTS` | 空 | 逗号分隔；追加进自动证书 SAN 的域名等 |
| 追加证书 IP | `-tls-ips` | `ADBREEZE_TLS_IPS` | 空 | 逗号分隔；追加进自动证书 SAN 的 IP |

```bash
# 环境变量示例（容器 / systemd 等场景）
ADBREEZE_PASSWORD=my-secret ADBREEZE_ADDR=9080 ./adbreeze
```

### HTTPS（默认开启）

WebCodecs（`VideoDecoder` / `AudioDecoder`）只在**安全上下文**暴露，即 HTTPS 或 `localhost`。用局域网 IP 以 `http://192.168.x.x:9080` 访问时，浏览器不提供该 API：视频会自动退化到 WebRTC 或 WASM 软解（音频同样会自动回退到 libopus WASM 解码，无需额外配置）。开启 HTTPS 后，这些访问也能走 WebCodecs 硬解。

```powershell
.\bin\adbreeze.exe                                # HTTP :9080 + HTTPS :9443 同时开启（默认）
.\bin\adbreeze.exe -https=false                   # 只开 HTTP :9080
.\bin\adbreeze.exe -https-addr :9444              # 自定义 HTTPS 端口

# 使用自己的正式证书
.\bin\adbreeze.exe -tls-cert cert.pem -tls-key key.pem
```

**HTTP 与 HTTPS 是并存关系，不是二选一**：HTTP 始终监听，作为保底入口——自签名证书在浏览器信任之前只能靠它进界面，且 HTTPS 起不来时不会导致完全失联。两者共用同一路由与鉴权，故**必须使用不同端口**（同端口会被启动即拦下并提示）。

#### 首次信任只需一次（IP / 主机名变化都免重导）

未指定 `-tls-cert` / `-tls-key` 时自动生成**本地 CA + 叶证书**两层结构，位置在**数据目录**（默认用户配置目录下的 `ADBreeze`，可用 `-data` / `ADBREEZE_DATA` 改）的 `tls/` 下：

| 文件 | 作用 | 是否变化 |
|---|---|---|
| `ca.pem` | 信任锚：**导入系统信任库的就是它** | 永不变化（指纹稳定）|
| `ca.key` | CA 私钥，用于签发叶证书 | 永不变化 |
| `cert.pem` / `key.pem` | 叶证书：SAN 覆盖当前主机名与全部网卡 IP | 名字集变化时**自动重签** |

关键在于**信任锚在 CA 上**，所以运行环境的主机名或 IP 变了（换网络、换机器名）只会让叶证书重签一次，CA 指纹不变，**你之前导入的信任继续有效，不需要再导一次**。（若用单个自签证书，每次名字变化都要重新签发、指纹改变、信任作废，环境一变就得再导一遍。）

数据目录必须**持久保存**：CA 文件一旦丢失（如被系统清理临时目录、容器重建），下次启动会生成全新 CA，已导入的信任作废。这也是默认数据目录放在用户配置目录、Docker 镜像默认写到 `/config` 卷的原因。从旧版本升级时，首次启动会自动把临时目录里的旧证书迁移到新位置（指纹不变，无需重新导入）。

启动日志会打印证书覆盖的主机名与 IP、以及 `ca.pem` 的路径。首次使用：

1. 把 `ca.pem` 导入系统信任库（Windows：`certmgr` → 受信任的根证书颁发机构 → 导入；需要管理员权限）
2. 重启浏览器

CA 与叶证书有效期均为 10 年，无需轮换。想彻底重置就删掉 `tls/` 目录后重启（会生成全新的 CA，此时需重新导入一次）。

> Windows 上的 `curl`（schannel）会因 CA 无吊销信息而报 `revocation status unknown`，这不影响浏览器——Chrome / Edge / Firefox 用自身逻辑校验，导入 CA 后即视为受信任。

#### 追加证书覆盖的主机名 / IP（环境变量）

自动探测只覆盖**本机网卡 IP 与主机名**。下面这些场景里，访问时用的名字探测不到，需要用环境变量显式追加进证书 SAN（逗号分隔，与自动探测取**并集**，localhost 等入口始终保留）：

| 场景 | 配置 |
|---|---|
| 自定义域名 / 反向代理域名 | `ADBREEZE_TLS_HOSTS=adb.example.com` |
| Docker 桥接 / NAT 后的外部地址 | `ADBREEZE_TLS_IPS=203.0.113.10` |

```bash
# 域名 + 外部 IP 一起配（等价命令行：-tls-hosts adb.example.com -tls-ips 203.0.113.10）
ADBREEZE_TLS_HOSTS=adb.example.com ADBREEZE_TLS_IPS=203.0.113.10 ./adbreeze
```

行为说明：

- 追加的名字与自动探测的名字享受同一套机制——**信任一次 CA 永久有效**：名字集变化只重签叶证书，CA 指纹不变，无需重新导入信任
- 非法 IP 只记日志并忽略，不影响启动；主机名写成 IP 形式（如 `ADBREEZE_TLS_HOSTS=192.168.1.5`）会自动按 IP 处理
- 仅在**自动生成证书**时有效；指定了 `-tls-cert` 时以该证书自身的 SAN 为准

### 安装为应用（PWA）

前端带 Web App Manifest 与 Service Worker，可作为独立应用安装：

- **桌面 Chrome / Edge**：地址栏右侧出现「安装」图标，或用菜单「应用 → 安装此站点为应用」
- **Android Chrome**：菜单「添加到主屏幕」，之后以独立窗口启动、无浏览器界面
- **iOS Safari**：分享 → 「添加到主屏幕」。已设 `apple-mobile-web-app-capable`，从主屏图标打开即为全屏，状态栏按 `black-translucent` 融入内容

Service Worker 只缓存静态外壳（HTML/CSS/JS/图标），**绝不缓存 `/api` 与 `/ws`**——那些是设备实时数据与实时流，缓存会导致看到陈旧状态或连接到失效会话。因此离线只能打开界面，功能仍需服务在线。

安装能力与安全上下文绑定：`http://localhost` 或 HTTPS 下可安装；用局域网 IP 以 `http://` 访问时浏览器不视为可安装站点（需 HTTPS 并信任 CA）。注意浏览器判定「可信 origin」看的是**主机名**而非它解析到的 IP：`localhost` 天然可信；写主机名（如 `http://my-pc:9080`）则必须走 HTTPS 且证书 SAN 得覆盖该主机名——这也是上面把主机名写进证书的原因。

### 非安全上下文（HTTP + 局域网 IP）下的能力

用 `http://192.168.x.x:9080` 这类地址访问时没有安全上下文，网页端各项能力的实际表现：

| 能力 | 表现 |
|---|---|
| 投屏画面 | 正常。自动降级：WebCodecs → WebRTC → WASM 软解（软解模式会自动让设备改用软件编码器，输出 Baseline 档）|
| 投屏音频 | 正常。自动回退到 libopus WASM 解码，复用同一条 AudioWorklet 播放管线 |
| 反向触控 / 终端 / 文件 / 应用 / 截图 | 正常（纯 HTTP 即可）|
| PWA 安装 | 不可用（需 HTTPS + 信任 CA）|

因此**换环境时不必依赖证书**：本机用 `http://localhost:9080`，其他设备用 `http://<IP>:9080` 即可完整使用；需要 PWA 安装或 WebCodecs 硬解时再走 HTTPS。

### 一键脚本

项目内的跨平台开发脚本 `scripts/dev.go`（Go 编写，Windows / macOS / Linux 通用）串起「前端构建 → Go 编译 → 启动」全流程：

```bash
go run ./scripts                              # 默认 admin/admin，http://localhost:9080 + https://localhost:9443
go run ./scripts -user xxx -password yyy      # 自定义账号密码
go run ./scripts -skip-web                    # 前端没改时跳过 pnpm build
go run ./scripts -https=false                 # 只开 HTTP（HTTP 始终可用）
go run ./scripts -addr :8000 -https-addr :9444  # 自定义端口
```

### 开发调试（前端热更新）

改前端代码时不必反复 `pnpm run build` + `go build`。Vite dev server 已配置代理（`/api` 与 `/ws` 转发到 `:9080` 的后端），两个终端分离运行即可：

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

预构建镜像已发布到 GHCR（`linux/amd64` + `linux/arm64` 双架构，拉取时自动匹配本机架构），直接运行即可：

```bash
docker pull ghcr.io/smanx/adbreeze:latest
docker run -d --name adbreeze \
  -p 9080:9080 -p 9443:9443 \
  -v adbreeze-config:/config \
  ghcr.io/smanx/adbreeze:latest
```

容器默认 `admin/admin`，浏览器打开 `http://localhost:9080`（或 `https://localhost:9443`）登录使用。容器内自带 adb，可 `adb connect` 管理网络设备（进入容器执行，或在网页上连接）。需要 USB 直连设备时追加设备透传参数：

```bash
docker run -d --name adbreeze -p 9080:9080 -p 9443:9443 \
  -v adbreeze-config:/config \
  --privileged -v /dev/bus/usb:/dev/bus/usb \
  ghcr.io/smanx/adbreeze:latest
```

**`-v adbreeze-config:/config` 是必须的**：容器内的数据目录 `/config` 存放自动生成的 TLS CA——不挂载的话，容器重建后 CA 重新生成、浏览器里导入的信任作废。挂载后 CA 跨容器存活，信任只需做一次。账号密码等配置可用环境变量传递（见上文配置表），无需改命令行：

```bash
docker run -d --name adbreeze -p 9080:9080 -p 9443:9443 \
  -v adbreeze-config:/config \
  -e ADBREEZE_PASSWORD=my-secret \
  ghcr.io/smanx/adbreeze:latest
```

版本标签与 Release 一致（如 `ghcr.io/smanx/adbreeze:v0.2.0`），`:latest` 始终指向最新发布。

也可从源码自行构建：

```bash
docker build -t adbreeze .
docker run -d --name adbreeze -p 9080:9080 -p 9443:9443 -v adbreeze-config:/config adbreeze
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

ADB 意味着设备的**完全控制权**。密码务必妥善保管（默认 `admin`，公网环境务必用 `-password` / `ADBREEZE_PASSWORD` 改掉），服务默认面向内网使用。Basic Auth 凭证以 Base64 明文编码传输，**切勿裸走 HTTP 暴露到公网**——HTTPS 默认已开启（自签名 CA 证书仅供内网；公网请用 `-tls-cert` / `-tls-key` 指定正式证书），或置于反向代理之后。会话 cookie 签名密钥每次启动随机生成，重启服务后所有已登录 WebSocket 会话需重新认证。

## 目录结构

```
adbreeze/
├── cmd/adbreeze/          # 入口
├── internal/
│   ├── adb/               # adb server 协议（smart socket + sync）
│   ├── scrcpy/            # 投屏（server 生命周期 / 协议解析 / ControlMessage / SPS）
│   ├── api/               # REST + WebSocket + 静态前端
│   ├── auth/              # Basic Auth + 会话 cookie
│   ├── settings/          # 用户偏好持久化（adb 路径等）
│   ├── tlsutil/           # 自签名证书生成 / 复用（HTTPS）
│   └── config/            # 命令行配置
├── web/dist/              # 前端（embed 进二进制）
│   └── (manifest.webmanifest / sw.js / icons/ 随构建产出，支撑 PWA 安装)
├── scripts/               # 跨平台开发脚本（go run ./scripts）
├── vendor-bin/            # 内嵌 scrcpy-server jar
├── Dockerfile / Makefile / .github/workflows/release.yml
```

## 致谢

- [scrcpy](https://github.com/Genymobile/scrcpy)（Apache-2.0）—— 投屏核心 `scrcpy-server` 与协议参考
- [ws-scrcpy](https://github.com/DeviceFarmer/ws-scrcpy) —— 协议实现的参考资料

## License

Apache-2.0
