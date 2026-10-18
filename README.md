[English](README_EN.md) | [简体中文](README.md)

# ADBreeze

**轻松管理你的安卓设备** —— 在 Windows / macOS / Linux / Docker / Android 上运行，打开浏览器即可全面控制任意 Android 设备：投屏、反向触控、终端、文件管理、剪贴板、截图、APK 安装。

> 本地只跑 ADB，所有功能 UI 全部在 Web 上。

项目地址：<https://github.com/smanx/adbreeze>

项目简介页（在线演示文档）：<https://smanx.github.io/adbreeze/#/about>

## 特性

- **远程控制**：既支持局域网直连，也支持通过 Cloudflare Tunnel 从外网访问，随时随地远程操作安卓手机
- **实时投屏**：基于 scrcpy 协议（H.264），浏览器 WebCodecs 硬解，低延迟；一路流支持多观众
- **音频转发**：设备声音（Opus）实时转发，浏览器 WebCodecs 解码 + AudioWorklet 播放，可静音
- **反向控制**：鼠标点击/拖动/滚轮、PC 键盘直接操作设备；旋转屏幕自适应（服务端解析 SPS 实时跟踪分辨率）
- **设备剪贴板互通**：读取/写入设备剪贴板，一键复制到本机
- **交互终端**：原生 PTY shell，前端 xterm.js 完整支持 vim/top 等交互程序
- **文件管理**：目录浏览、上传下载、删除、新建（原生 adb sync 协议实现）
- **应用管理**：应用列表、启动 / 停止 / 卸载、导出 APK
- **设备信息 / 性能监控**：系统概览、CPU / 内存实时曲线
- **进程管理**：进程列表与一键结束
- **Logcat 日志**：浏览器内实时查看设备日志
- **截图 / APK 安装**：一键 screencap 下载 PNG；浏览器拖入 APK 直接安装
- **批量命令**：勾选多台设备并行执行 shell
- **设备热插拔**：`host:track-devices` 长连接监听，设备上下线实时推送
- **单文件交付**：前端与 scrcpy-server 全部嵌入二进制，部署 = 拷贝一个文件
- **adb server 自愈**：检测到 adb server 消失时自动拉起
- **外网访问（可选）**：设置页一键开启 Cloudflare Tunnel，服务即刻获得公网 HTTPS 地址——不需要公网 IP、不开路由器端口；cloudflared 不内置（设置里可一键安装），安装包零增长

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
- **投屏**：嵌入 `scrcpy-server` v5.0.1，reverse 隧道优先（无连接竞态）、forward 自动回退；视频流经 Hub 扇出给多个 WebSocket 订阅者，新订阅者自动补发 SPS/PPS 并对齐关键帧
- **鉴权**：管理员密码登录（页面内登录表单），登录后凭 12 小时会话（cookie / Bearer token）访问，支持登出、防爆破锁定

## 控制流程

两种典型部署形态：

**场景一：服务端运行在被控端** —— 全部装在一台安卓手机上

```
┌─ 同一台安卓设备 ─────────────┐
│  被控端 = 服务端             │
│  （ADBreeze APK 内运行       │
│    adb server + Go 服务）    │
└──────────────┬───────────────┘
               │ 局域网直连 或 CF 穿透（HTTP / HTTPS）
               ▼
        控制端：浏览器（手机本机或局域网内任意电脑）
```

APK 在手机上同时运行 adb server 与 Go 服务；用手机本机或局域网内任意电脑的浏览器打开即可控制。

**场景二：服务端与被控端分离** —— 服务端跑在电脑 / Docker / 另一台安卓上

```
┌─ 被控端 ─────────┐                  ┌─ 服务端 ─────────────┐
│ 手机 / 平板       │                  │ Windows              │
│ 电视盒子 / 车机   │ ⇄ USB / Wi-Fi ⇄ │ macOS / Linux        │ ⇄ HTTP/HTTPS ⇄ 控制端
│ 模拟器 / 更多     │  （adb 连接）     │ Docker               │        （任意浏览器）
└──────────────────┘                  │ 安卓（APK）          │
                                      └──────────────────────┘
```

一个 Go 二进制经 USB / adb connect 管理任意多台安卓设备；可经局域网直连或 Cloudflare Tunnel 访问。控制端是任意现代浏览器（可装为 PWA），与承载服务端的机器无关——Windows、macOS、iOS、Linux、安卓、鸿蒙等设备均可。

## 快速开始

**直接下载**：无需构建，到 [GitHub Releases](https://github.com/smanx/adbreeze/releases) 下载对应平台的单文件（Windows 双击即用，见下文），或拉取预构建 Docker 镜像（见 [Docker](#docker) 一节）。

**从源码构建**：前端产物 `web/dist` 通过 `go:embed` 嵌入二进制，且不随仓库分发（见 `.gitignore`），因此**克隆后首次构建需先构建前端**（依赖用 [pnpm](https://pnpm.io) 管理）：

```powershell
# 1. 构建前端（Node 18+ / pnpm 10+）
cd web
pnpm install
pnpm run build
cd ..

# 2. 构建（Go 1.26+；或 `make build`，自动先构建前端）
go build -o bin/adbreeze.exe ./cmd/adbreeze

# 3. 运行：所有参数都可省略，默认密码 admin，HTTP :9080 + HTTPS :9443
.\bin\adbreeze.exe

# 需要时用参数覆盖（优先级：命令行 > 环境变量 > 默认值）
.\bin\adbreeze.exe -addr :8000 -https=false -password my-secret
```

**Windows 直接双击 `adbreeze-windows-amd64.exe` 即可运行**：无参数启动 = 默认配置，启动成功后自动打开默认浏览器进入管理界面，控制台窗口保留运行日志（关掉窗口即停止服务）。控制台里运行、带参数运行同样支持——带参数启动属于「控制台 / 服务场景」，此时不会自动开浏览器（需要时显式加 `-open-browser`）。

> 首次运行如遇 SmartScreen 拦截（「Windows 保护了你的电脑」），点「更多信息 → 仍要运行」即可；未做代码签名的开源软件都会走这个提示。启动失败时窗口会停住并提示原因、按回车退出，不会一闪而过。

> **前提条件**：本项目基于 ADB 实现对设备的远程控制，因此被控端 Android 设备需先开启**开发者模式**并允许 **USB 调试**（通过网络 `adb connect` 连接时还需开启**无线调试**），否则设备无法被发现与控制。

浏览器打开 `http://localhost:9080`（HTTPS 在 `https://localhost:9443`），在页面登录框输入管理员密码即可使用。

### 配置（命令行 / 环境变量 / 默认值）

三项来源优先级从高到低：**命令行参数 > 环境变量 > 默认值**。全部可省略。

| 配置 | 命令行 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|---|
| 管理员密码 | `-password` | `ADBREEZE_PASSWORD` | `admin` | 页面登录用；`-user` 已废弃（仅为兼容旧脚本保留，无作用） |
| HTTP 端口 | `-addr` | `ADBREEZE_ADDR` | `:9080` | HTTP 始终开启；只写端口号（如 `9080`）也可以 |
| HTTPS 开关 | `-https` | `ADBREEZE_HTTPS` | `true` | `-https=false` 关闭（HTTP 始终可用）|
| HTTPS 端口 | `-https-addr` | `ADBREEZE_HTTPS_ADDR` | `:9443` | 与 HTTP 必须不同端口 |
| adb 路径 | `-adb` | `ADBREEZE_ADB` | PATH 查找 | PATH 找不到时自动补探常见安装位置（见下）；也可在网页设置里随时切换 |
| 自动打开浏览器 | `-open-browser` | `ADBREEZE_OPEN_BROWSER` | 见说明 | 无参数启动（双击 / Finder）默认开启；带参数启动 / Linux 默认关闭 |
| 数据目录 | `-data` | `ADBREEZE_DATA` | 用户配置目录下的 `ADBreeze` | 存放 TLS 证书，需持久保存 |
| TLS 证书 | `-tls-cert` / `-tls-key` | `ADBREEZE_TLS_CERT` / `ADBREEZE_TLS_KEY` | 自动生成 | 指定正式证书时成对使用 |
| 追加证书主机名 | `-tls-hosts` | `ADBREEZE_TLS_HOSTS` | 空 | 逗号分隔；追加进自动证书 SAN 的域名等 |
| 追加证书 IP | `-tls-ips` | `ADBREEZE_TLS_IPS` | 空 | 逗号分隔；追加进自动证书 SAN 的 IP |
| 外网隧道开关 | `-tunnel` | `ADBREEZE_TUNNEL` | `false` | 启动即开启 Cloudflare Tunnel（无头场景用；完整配置在设置页）|
| 外网隧道令牌 | `-tunnel-token` | `ADBREEZE_TUNNEL_TOKEN` | 空 | Named 模式令牌；空 = Quick Tunnel 随机地址 |
| WebRTC STUN | `-webrtc-stun` | `ADBREEZE_WEBRTC_STUN` | 两个公共 STUN | 逗号分隔；`off` 关闭；自建 coturn / TURN 在此覆盖 |

```bash
# 环境变量示例（容器 / systemd 等场景）
ADBREEZE_PASSWORD=my-secret ADBREEZE_ADDR=9080 ./adbreeze
```

### 自动打开浏览器（双击运行）

`-open-browser` 默认开启，但**只在「无参数启动」时自动生效**——这正是 Windows 双击 / macOS Finder 启动的场景：用户就在电脑前，服务就绪后直接把管理界面带到默认浏览器面前，不用对着控制台猜地址。判据与规则：

| 启动方式 | 是否自动开浏览器 |
|---|---|
| 双击 exe / Finder 启动（无参数） | 开 |
| 控制台 / 服务器带参数启动（`-addr` 等） | 不开（需要时显式加 `-open-browser`） |
| Linux（含 Docker / Termux） | 不开（无桌面浏览器；显式指定才尝试 `xdg-open`） |
| 显式 `-open-browser=false` 或 `ADBREEZE_OPEN_BROWSER=false` | 一律不开 |

自动开浏览器只是便利性兜底：失败只记日志，不影响服务本身，手动访问启动日志里打印的地址即可。Windows 上启动时还会把控制台代码页切到 UTF-8（中文日志不变乱码），退出时自动还原，不影响你在同一个 cmd 窗口里的后续操作。

### adb 自动探测

`-adb` 留空时按以下顺序找 adb：**PATH → 常见安装位置**。常见位置覆盖 `ANDROID_HOME` / `ANDROID_SDK_ROOT`、可执行文件同级的 `platform-tools`、以及各平台标准 SDK 目录（Windows：`%LOCALAPPDATA%\Android\Sdk\platform-tools` 等；macOS：`~/Library/Android/sdk/platform-tools`；Linux：`~/Android/Sdk/platform-tools`）。PATH 能找到时行为与以前完全一致，探测只在 PATH 失败时补漏——Windows 上 adb 极少加进 PATH，没这一步双击运行会看不到任何设备。找到后日志会打印实际使用的路径；要指定其他位置，用 `-adb` 或在网页「设置」里切换（立即生效，无需重启）。

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

启动日志会打印证书覆盖的主机名与 IP、以及 `ca.pem` 的路径。首次使用（二选一）：

1. **界面内下载安装（推荐）**：网页「设置 → HTTPS 证书」里直接下载 CA 证书（`.crt`）或 iOS 描述文件（`.mobileconfig`），页内附各平台（Windows / macOS / Linux / 安卓 / iOS）逐步安装方法与 CA 指纹核对
2. **手动导入**：把数据目录 `tls/ca.pem` 导入系统信任库（Windows：`certmgr` → 受信任的根证书颁发机构 → 导入；需要管理员权限），然后重启浏览器

安装一次即可：CA 长期不变，之后换网络、换 IP、重启服务都不需要再装。iOS/iPadOS 注意装完描述文件还必须到「设置 → 通用 → 关于本机 → 证书 trust 设置」打开完全信任，两步缺一不可。

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

### 外网访问（Cloudflare Tunnel）

没有公网 IP、不想做端口映射时，可以用 Cloudflare Tunnel 把服务发布到公网：cloudflared 只**主动向外建立出站连接**，不监听任何入站端口，家用宽带 / 公司 NAT 后面都能用。完整设计与安全考量见 [`CF-TUNNEL-DESIGN.md`](CF-TUNNEL-DESIGN.md)。

**cloudflared 不内置**（安装包 +0）。两种装法，任选其一：

1. **一键安装（推荐）**：设置 → 外网访问 → 点「一键安装」——从 Cloudflare 官方 GitHub releases 下载当前平台（服务端 `GOOS/GOARCH`，未必等于你浏览器所在平台）的二进制到数据目录 `bin/`，装完自动探测到，无需重启；可随时重跑以更新。安装后界面会显示版本与 SHA256 供核对
2. **手动安装**：

| 平台 | 安装 |
|---|---|
| Windows | `winget install --id Cloudflare.cloudflared` |
| macOS | `brew install cloudflared` |
| Linux | 官方 release 页的 deb / rpm / 二进制 |

然后在**设置 → 外网访问（Cloudflare Tunnel）**里确认 cloudflared 已就绪（显示路径与版本），点「开启」即可获得 `https://<随机词>.trycloudflare.com` 地址（Quick Tunnel，无需 Cloudflare 账号）。地址随机、不分享即不可枚举；Cloudflare 边缘提供受信任证书，浏览器侧就是安全上下文，且**不需要信任本地 CA**。一键安装与 winget / brew 同源（固定官方仓库 + HTTPS；Cloudflare 未随 release 发布校验和文件，故不做外部 checksum 比对，以安装后 `--version` 冒烟 + SHA256 展示替代）。

两种模式：

| | Quick Tunnel（默认） | Named Tunnel |
|---|---|---|
| 账号 | 不需要 | 需要（免费） |
| 地址 | 随机，重启即变 | 固定域名（CF 后台绑定） |
| 前置防护 | 无 | 可叠加 Cloudflare Access（邮箱 OTP / Google 登录） |

Named 模式：设置页填入 CF 后台的 tunnel token 即可。**建议给固定域名叠加 Cloudflare Access**，给公网入口加第二道身份验证。完整步骤如下。

#### 固定域名 + Cloudflare Access（Named Tunnel）

前置条件：一个域名已接入 Cloudflare（DNS 由 CF 托管，免费套餐即可）。

**1. 创建隧道拿 token**（二选一）：

- 后台：Cloudflare Zero Trust 控制台 → Networks → Tunnels → Create tunnel → 选 cloudflared → 起个名字（如 `adbreeze`）→ 创建后在安装页面复制 token（`eyJ...` 开头的一长串）
- CLI：`cloudflared tunnel login` → `cloudflared tunnel create adbreeze` → `cloudflared tunnel token adbreeze`

**2. 绑定公网主机名**：Tunnel → Public Hostname → Add：子域名（如 `adb.example.com`）→ 类型选 **HTTP** → 地址填 `127.0.0.1:9080`（ADBreeze 的 HTTP 端口；边缘侧由 CF 提供受信任证书，本地无需配证书）。也可用 CLI：`cloudflared tunnel route dns adbreeze adb.example.com`，入口规则写在 Cloudflare 后台或本机 `config.yml` 均可——**ADBreeze 只认 token**，入口配置以 CF 后台为准。

**3. 填入 ADBreeze**：设置 → 外网访问 → 隧道令牌里粘贴 token → 保存 → 开启。界面显示「Named Tunnel（固定域名）」即生效，手机电脑访问 `https://adb.example.com` 直达登录页。

**4. 叠加 Access（推荐）**：Zero Trust → Access → Applications → Add an application → Self-hosted：应用名随意，子域选 `adb.example.com`；Policy 选 Allow，加一条你的邮箱（一次性 PIN）或 Google 登录。之后任何人访问该域名，先过 Cloudflare 的身份验证，通过才到达 ADBreeze 登录页——**即使 ADBreeze 密码泄露也有第二道门**。Access 免费额度个人够用；策略随时可在后台改。

注意：Access 只保护 Named Tunnel（需要域名 zone）；Quick Tunnel 的随机地址用不了，靠「不分享即不可枚举」+ 强密码兜底。

安全约束（提示，不拦截——与局域网「默认密码只警告」策略一致）：

- **默认密码 `admin` 也能开启**，但开启时服务端会记 WARN 日志、界面展示提醒：公网地址 + 可猜密码风险大，建议用 `-password` / `ADBREEZE_PASSWORD` 改强密码（重启生效）
- 隧道默认打 `http://127.0.0.1:<HTTP端口>`：与「服务是否对外监听」解耦，把 `-addr` 收紧到仅本机也不影响外网访问
- token 只经子进程环境变量传递（不进进程参数、不进日志、API 只回脱敏预览），明文存于本机 `settings.json`（权限 0600）
- 隧道运行期间**顶栏常驻「外网访问中」徽章**，任何页面都看得见此刻正暴露在公网
- **自启 = 记住上次状态**：开启过且没有手动「关闭」（含直接退出 / 崩溃）→ 下次启动 ADBreeze 自动恢复隧道；从未开启、或手动关闭过一次 → 不再自启，直到再次手动开启。想彻底关掉：开一次、关一次即可
- 投屏经外网时：信令走隧道没问题；WebRTC 媒体是 P2P 不过隧道——默认已配公共 STUN（`stun.cloudflare.com:3478` + `stun.l.google.com:19302`，`-webrtc-stun` 可覆盖/`off` 关闭），两端可打洞时自动走 P2P；对称 NAT 等打不通的场景退化到 WS 视频通道（能用，延迟更高），要根治需自建 TURN

无头场景（Docker / Termux，没有浏览器 UI）用启动参数：`-tunnel` 开启、`-tunnel-token <token>` 指定 Named 模式；其余配置（cloudflared 路径、协议、自动重启）仍走设置页存档，存档优先、token 缺省时回退到命令行值。`-tunnel` 开启同样会记住状态（下次无 flag 也恢复）。

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

> ⚠️ **AI Agent 注意**：有两个启动脚本，使用对象不同，请按身份选择——
> - **`go run ./scripts/agent`**：AI Agent / 自动化工具专用（端口 9081/9444，不与用户实例冲突）；
> - **`go run ./scripts`**：仅供用户手动使用，**AI Agent 不要调用**。
>
> 两者仅默认端口不同，参数完全一致。

项目内的跨平台开发脚本（Go 编写，Windows / macOS / Linux 通用）串起「前端构建 → Go 编译 → 启动」全流程：

```bash
# 用户手动使用（默认端口 9080/9443）
go run ./scripts                              # 默认密码 admin，http://localhost:9080 + https://localhost:9443
go run ./scripts -password yyy                # 自定义管理员密码
go run ./scripts -skip-web                    # 前端没改时跳过 pnpm build
go run ./scripts -https=false                 # 只开 HTTP（HTTP 始终可用）
go run ./scripts -addr :8000 -https-addr :9444  # 自定义端口

# AI Agent 专用（默认端口 9081/9444，可与用户实例并行运行）
go run ./scripts/agent
```

### 开发调试（前端热更新）

改前端代码时不必反复 `pnpm run build` + `go build`。Vite dev server 已配置代理（`/api` 与 `/ws` 转发到 `:9080` 的后端），两个终端分离运行即可：

```bash
# 终端 1：起后端（或直接 go run ./scripts -skip-web）
./bin/adbreeze -password admin                 # Windows 为 .\bin\adbreeze.exe

# 终端 2：起前端开发服务器
cd web
pnpm run dev
```

浏览器打开 `http://localhost:5173`，在页面登录框输入管理员密码 `admin`。此后修改 `web/src` 下的代码保存即生效（HMR 热更新，投屏会话不中断）；只有改动 Go 代码时才需要重启终端 1 的后端。注意 `pnpm run dev` 与一键脚本的前端构建不要同时进行（都会写 `web/dist`）。

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

容器默认密码 `admin`，浏览器打开 `http://localhost:9080`（或 `https://localhost:9443`）登录使用。容器内自带 adb，可 `adb connect` 管理网络设备（进入容器执行，或在网页上连接）。需要 USB 直连设备时追加设备透传参数：

```bash
docker run -d --name adbreeze -p 9080:9080 -p 9443:9443 \
  -v adbreeze-config:/config \
  --privileged -v /dev/bus/usb:/dev/bus/usb \
  ghcr.io/smanx/adbreeze:latest
```

**`-v adbreeze-config:/config` 是必须的**：容器内的数据目录 `/config` 存放自动生成的 TLS CA——不挂载的话，容器重建后 CA 重新生成、浏览器里导入的信任作废。挂载后 CA 跨容器存活，信任只需做一次。密码等配置可用环境变量传递（见上文配置表），无需改命令行：

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
./adbreeze-linux-arm64-*        # 运行服务（产物名带版本号）
adb connect 127.0.0.1:5555      # 无线调试自连
```

### Android（APK）

与桌面版形态一致：**App 只是一个服务控制台**——填端口 / 管理员密码，点「启动」，日志就地显示；界面不内置，用浏览器打开服务地址（App 里有一键「打开页面」）操作设备。

- **获取**：GitHub Release 直接下载 `adbreeze-android-<版本>.apk`（打 tag 后 CI 自动随桌面平台一起打进 release，所有产物文件名均带版本号）；或本地 `make android-apk` 自行打包
- 打开 App → 填配置（默认 9080 / admin）→ 点「启动」→ 浏览器访问 `http://127.0.0.1:<端口>`（局域网设备可访问 `http://<手机IP>:<端口>`，日志里会列出）
- **默认参数与 PC 端一致**：HTTP + HTTPS 双端口（HTTPS :9443），启动后状态栏绿点标记；局域网设备要硬解投屏需在网页设置页下载 CA 并导入信任（不导入则用 HTTP 访问，投屏走 WebRTC/软解）
- 连接方式为**无线**：目标设备开「开发者选项 → 无线调试」，在网页里 `adb connect <ip>:<端口>`；也可 `adb connect 127.0.0.1:5555` 投本机自己
- 常驻通知显示服务地址、可「停止服务」；进程/端口掉线自动重拉
- 架构与实现细节见 [ANDROID.md](ANDROID.md)；CI 见 `.github/workflows/release.yml`（release 统一出包）与 `.github/workflows/android.yml`（push 构建检查）

## API 概览

HTTP 接口通过会话认证：先 `POST /api/session`（body `{"password": "..."}`）用管理员密码换取会话，之后凭 `HttpOnly` 会话 cookie（同源自动携带）或 `Authorization: Bearer <token>`（跨源）访问；WebSocket 握手凭 cookie 或 `?session=<token>` 查询参数。`/api/health` 与 `/api/session` 之外的接口均受保护。会话 12 小时有效，`DELETE /api/session` 登出。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/devices` | 设备列表 |
| POST / DELETE | `/api/devices/connect`、`/api/devices/{serial}` | 网络设备连接 / 断开 |
| POST | `/api/devices/{serial}/shell` | 执行一次性命令 |
| POST / GET / DELETE | `/api/devices/{serial}/mirror` | 投屏启动 / 信息 / 停止 |
| POST | `/api/devices/{serial}/webrtc` | 建立 WebRTC 视频通道（回退 / 外网 P2P） |
| GET | `/api/devices/{serial}/overview` | 设备系统概览（型号 / 电量 / 存储等） |
| GET | `/api/devices/{serial}/perf` | 性能采样（CPU / 内存） |
| GET | `/api/devices/{serial}/ui-dump` | UI 层级 dump（控件树） |
| GET / POST | `/api/devices/{serial}/apps`、`/apps/info`、`/apps/action`、`/apps/apk` | 应用列表 / 详情 / 启停卸载 / 导出 APK |
| GET / POST | `/api/devices/{serial}/processes`、`/processes/kill` | 进程列表 / 结束进程 |
| WS | `/ws/video/{serial}`、`/ws/audio/{serial}` | 视频流（下行二进制帧，上行 JSON 控制事件） / 音频流 |
| WS | `/ws/term/{serial}`、`/ws/logcat/{serial}` | 交互终端 / 实时日志 |
| WS | `/ws/events` | 设备上下线推送 |
| GET/PUT/DELETE | `/api/devices/{serial}/files` | 文件浏览 / 上传 / 删除 |
| GET | `/api/devices/{serial}/files/content` | 文件下载 |
| POST | `/api/devices/{serial}/files/mkdir`、`/files/rename` | 新建目录 / 重命名 |
| GET/POST | `/api/devices/{serial}/clipboard` | 剪贴板读取 / 写入 |
| GET | `/api/devices/{serial}/screenshot` | 截图 PNG |
| POST | `/api/devices/{serial}/install` | APK 安装 |
| GET/PUT | `/api/settings/tunnel` | 外网访问（Cloudflare Tunnel）配置读写 |
| POST / GET | `/api/settings/tunnel/install` | 一键安装 cloudflared（触发 / 查进度） |
| GET | `/api/tunnel` | 隧道状态（连接态 / 外网 URL / 日志） |
| POST | `/api/tunnel/start`、`/api/tunnel/stop` | 隧道开启 / 关闭 |

## 安全提示

ADB 意味着设备的**完全控制权**。密码务必妥善保管（默认 `admin`，公网环境务必用 `-password` / `ADBREEZE_PASSWORD` 改掉；默认密码 + 对外监听时启动日志会给出安全警告），服务默认面向内网使用。**切勿裸走 HTTP 暴露到公网**——HTTPS 默认已开启（自签名 CA 证书仅供内网；公网请用 `-tls-cert` / `-tls-key` 指定正式证书），或置于反向代理之后。管理员密码只在登录时提交一次，之后传输的是 12 小时过期的会话 token；会话签名密钥每次启动随机生成，重启服务后所有已登录会话需重新认证。登录接口内置防爆破（连续失败 5 次锁定 30 秒）。

## 目录结构

```
adbreeze/
├── cmd/adbreeze/          # 入口（launch.go 双击体验 / console_*.go 控制台代码页）
├── internal/
│   ├── adb/               # adb server 协议（smart socket + sync）
│   ├── scrcpy/            # 投屏（server 生命周期 / 协议解析 / ControlMessage / SPS）
│   ├── webrtc/            # WebRTC 会话（非安全上下文 / 外网场景的视频回退）
│   ├── api/               # REST + WebSocket + 静态前端
│   ├── auth/              # 管理员密码登录 + 会话 cookie/token
│   ├── browser/           # 调起系统默认浏览器（双击运行的收尾体验）
│   ├── settings/          # 用户偏好持久化（adb 路径 / 隧道配置，0600 落盘）
│   ├── tlsutil/           # 自签名证书生成 / 复用（HTTPS）
│   ├── tunnel/            # 外网访问：托管 cloudflared 子进程（CF-TUNNEL-DESIGN.md）
│   └── config/            # 命令行配置
├── web/                   # 前端源码（React + Vite）
│   └── dist/              # 前端构建产物（embed 进二进制，不随仓库分发；
│                          #   manifest.webmanifest / sw.js / icons/ 随构建产出，支撑 PWA）
├── android/               # Android App（Go 服务端嵌入 assets，APK 形态）
├── scripts/               # 跨平台开发脚本：go run ./scripts（用户手动）/ go run ./scripts/agent（AI Agent 专用）
├── vendor-bin/            # 内嵌 scrcpy-server jar
├── Dockerfile / Makefile / .github/workflows/release.yml
```

## 致谢

- [scrcpy](https://github.com/Genymobile/scrcpy)（Apache-2.0）—— 投屏核心 `scrcpy-server` 与协议参考
- [ws-scrcpy](https://github.com/DeviceFarmer/ws-scrcpy) —— 协议实现的参考资料

> 注意：本项目代码以 MIT 协议发布；内嵌的 `scrcpy-server` 仍遵循其原 Apache-2.0 协议。

## License

[MIT](LICENSE)
