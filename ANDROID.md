# ADBreeze 安卓打包方案：APK 直启服务

> 目标：把 adbreeze 打包成安卓 APK，**点击图标即启动完整服务并用界面操作设备**。
> 约束：**Go 主程序零代码改动**（只新增一个 Android 子工程 + 构建脚本）。

---

## 0. 结论（TL;DR）

采用 **「Kotlin 薄壳 APK + Go 二进制子进程 + 内置 adb + WebView 承载现有前端」**：

```
APK 图标点击
   └─▶ 前台 Service 启动
         ├─ ① 用 /system/bin/linker64 拉起内置 adb → adb server 监听 127.0.0.1:5037
         ├─ ② 用 linker64 启动 Go 服务子进程（环境变量注入全部配置）
         └─ ③ 等 9080 端口就绪 → WebView 加载 http://127.0.0.1:9080（自动过 Basic Auth）
```

Go 侧不需要改一行代码：所有配置项已有环境变量入口（`ADBREEZE_ADDR` / `ADBREEZE_DATA` / `ADBREEZE_HTTPS` / `ADBREEZE_ADB` …），WebView 里跑的就是现有 `web/dist` 前端。

---

## 1. 备选方案对比（为什么选它）

| 方案 | 可行性 | 结论 |
|---|---|---|
| A. Termux 里跑 linux/arm64 二进制（README 已记载） | 可行 | 不满足「APK 直启」需求，仅作为开发期调试手段保留 |
| B. gomobile bind 编成 .aar，Go 跑在 app 进程内 | 差 | **必须改代码**：`flag.Parse()` 会解析 Android 进程参数直接报错退出；`exec.Command(adb start-server)` 同样撞 Android 10+ 的 exec 限制；`signal.NotifyContext` 在 app 内语义不对 |
| **C. APK 壳 + Go 子进程（本方案）** | **好** | **Go 零改动**，壳只做「进程管理 + WebView」两件事；配置全靠环境变量 |
| D. 原生重写（Compose 直连 adb/scrcpy 协议） | 工作量巨大 | 等于把整个 Go 后端翻译成 Kotlin，不划算 |
| E. andronix / proot 容器发行版 | 差 | 体积、性能、体验全面劣于 C |

**选 C 的决定性理由**：adbreeze 的服务端本来就是「单文件进程 + 浏览器 UI」的形态——APK 化只是把「启动进程」和「打开浏览器」两步交给 Kotlin 壳，Pipeline 一行不改。

---

## 2. 总体架构

```
┌─────────────── APK (com.smanx.adbreeze) ───────────────┐
│  Kotlin 壳                                              │
│  ┌──────────────┐   ┌───────────────────────────────┐  │
│  │ MainActivity │──▶│ ServerService (前台服务保活)    │  │
│  │  (WebView)   │   │  ├ linker64 exec adb ──▶ adb   │  │
│  └──────┬───────┘   │  │      server :5037           │  │
│         │           │  ├ linker64 exec adbreeze ─▶ Go│  │
│    http://127.0.0.1:9080 服务子进程 :9080             │  │
│         │           │  └ 看护协程(掉线重拉)           │  │
│         │           └───────────────────────────────┘  │
│  assets/：adbreeze(Go,arm64) + adb(arm64)+依赖 so       │
└────────────────────────┬───────────────────────────────┘
                         │ adb connect <ip>:<port>（无线）
                         ▼
               ┌───────────────────┐
               │  目标安卓设备      │  （scrcpy-server.jar 由 Go 经
               │  无线调试 / 5555   │   sync 协议推送，app_process 执行）
               └───────────────────┘
```

数据流与桌面版完全一致：WebView ⇄(HTTP/WS 127.0.0.1)⇄ Go 进程 ⇄(smart socket/sync 协议 127.0.0.1:5037)⇄ adb server ⇄(TCP/WiFi)⇄ 目标设备。

---

## 3. 关键技术决策与坑（本方案的全部难点）

### 3.1 Android 10+ 不能直接 execve 应用私有目录里的二进制 —— 用 linker64 包装

SELinux 对 `untrusted_app` 域禁掉了 `execute app_data_file`（Termux 的 `termux-exec` 就是为了绕它而生）。直接 `Runtime.exec()` 启动 APK 资产里的二进制会 `Permission denied`。

**解法**（已验证手法，Termux 同款）：

```kotlin
// 目标 PIE 二进制（Go android/arm64 默认就是 PIE）
val argv = arrayOf("/system/bin/linker64", binaryPath, *args)
Runtime.getRuntime().exec(argv)
```

Go 服务进程、adb 进程都走这个包装。兜底策略：先试直接 `exec(路径)`，失败再降级 linker64（个别 ROM 表现不一）。

### 3.2 adb server 必须由壳「前置拉起」（最关键的时序设计）

Go 的 `EnsureServer()` 内部是 `exec.Command(adb, "start-server")`；Go 子进程同样跑在 `untrusted_app` 域，这个 execve 在 Android 10+ 大概率被拦。

**但这不是问题**——adb server 只需要 5037 端口上有进程在听，谁来拉起无所谓：

1. Service 先执行 `adb start-server`（linker64）。adb 会 fork 出 server 守护进程后退出；fork 出的进程无新 execve、同域，合规。
2. 确认 `127.0.0.1:5037` 可连后，才启动 Go 服务。
3. Go 启动时 `Client.Ping()` 直接成功 → **永远走不到内部 exec 分支**，机制上消灭了这个问题。
4. Server 若之后被杀：壳内看护协程每 30s ping 5037，掉了就用 linker64 重新 `adb start-server`（比依赖 Go 内置的自愈更可靠，因为 Go 那条路被 SELinux 挡着）。

目标端 adbd 走无线调试，完全没有 USB 依赖——这正是安卓端唯一可行的连接方式（见 3.7）。

### 3.3 adb 二进制从哪来

- **首选：Termux 的 `android-tools` 包**（arm64）。下载其 .deb，取出 `bin/adb` 及依赖 `.so`（`libc++_shared.so`、视版本可能还有 `libcrypto`/`libprotobuf` 等），全部打进 `assets/adb/arm64-v8a/`。
- 运行期：linker64 执行 adb，并给子进程设置 `LD_LIBRARY_PATH=<解压目录>`（Termux 的 termux-exec 也是这么修 LD_LIBRARY_PATH 的）。
- 验收标准：`adb version` 正常输出；`adb start-server` 后 `adb devices`/`adb connect` 可用。
- 备选（不推荐一期做）：NDK 自编 AOSP `packages/modules/adb`（要静态链 libcrypto，工作量大）。

### 3.4 配置注入（Go 零改动的核心）

| 环境变量 | 值 | 说明 |
|---|---|---|
| `ADBREEZE_ADDR` | `:9080` | HTTP 服务；`127.0.0.1:9080` 给壳的 WebView 用，绑全接口则局域网其他机器也能访问（默认） |
| `ADBREEZE_HTTPS` | `false` | **关掉 HTTPS**：`http://127.0.0.1` 本身就是浏览器安全上下文，WebCodecs 可用；免去整套自签 CA 逻辑 |
| `ADBREEZE_DATA` | `<filesDir>` | settings.json 等落地（持久目录） |
| `HOME` | `<filesDir>` | Go 的 `os.UserConfigDir()` 在 Android 依赖 `$HOME`，不设则 settings 退化为内存态（不致命，但设了能持久化界面里改的 adb 路径） |
| `ADBREEZE_ADB` | `<assets 解压出的 adb 路径>` | 让设置页直接可见/可改的默认 adb 路径 |
| `ADBREEZE_PASSWORD` | `admin`（后可改设置页） | 页面登录用的管理员密码 |

### 3.5 WebView 承载现有前端（UI 零改动）

- 加载 `http://127.0.0.1:9080`；`javaScriptEnabled`、`domStorageEnabled`、`databaseEnabled` 全开。
- **登录**：页面内置登录表单（管理员密码 → `POST /api/session` 换会话），WebView 无需额外处理；同源会话 cookie 由浏览器自动携带，fetch / WS 握手都带上凭证。
- 能力体检（Android System WebView / Chrome 94+ 均支持）：WebSocket、WebCodecs VideoDecoder/AudioDecoder、WebRTC（RTCPeerConnection）、AudioWorklet、h264bsd WASM 软解兜底。
- 已知小差异：`requestFullscreen` 对非 `<video>` 元素在 WebView 里无动画（前端有兜底探测）；必要时壳补 `onShowCustomView/onHideCustomView`，实测后决定。
- 备选路线：壳只起服务、然后 `Intent.ACTION_VIEW` 调系统浏览器打开——更省事但脱离「APK 内」体验，作为 Plan B。

### 3.6 保活（进程不能随界面退出而消失）

- **前台 Service**：`android:foregroundServiceType="specialUse"` + `<property>` 声明用途（Android 14/15 要求）；`android:stopWithTask="false"`，Activity finish 后服务继续跑。
- 通知栏常驻：显示 `http://127.0.0.1:9080`，操作按钮「打开界面 / 停止服务」。
- 可选：引导用户加电池优化白名单（不强制）。

### 3.7 能力边界（必须写进 README / 界面提示）

1. **只能无线连接设备**。adb server 跑在 Android 上时，SELinux 禁止 `untrusted_app` 访问 `/dev/bus/usb/*`，USB OTG 接目标设备不可行。目标机走「开发者选项 → 无线调试」→ 网页里 `adb connect <ip>:<port>`。
2. **投本机自己**：手机开自身无线调试（5555），`adb connect 127.0.0.1:5555`（README Termux 章节已有此用法）。
3. 其余功能（投屏/反向触控/终端/文件/剪贴板/截图/装 APK）全部只依赖 TCP 隧道，**与桌面版能力一致**。
4. 服务默认绑 `0.0.0.0:9080`：同局域网其他电脑/手机浏览器也能打开界面——这是能力增值，但也意味着默认 admin/admin 会暴露在局域网，正式版应在设置里提供改密码/改绑定的开关。

---

## 4. APK 工程结构（新增，不动主仓库代码）

```
adbreeze/
├── （现有 Go / web 全部不动）
├── Makefile                    # 新增 android-* 目标（cross compile）
├── .github/workflows/release.yml # 扩展：追加 APK job
└── android/                    # 新建 Gradle 工程
    ├── settings.gradle.kts
    └── app/
        ├── build.gradle.kts     # minSdk 26 / targetSdk 35 / extractNativeLibs
        └── src/main/
            ├── AndroidManifest.xml
            ├── assets/
            │   ├── adbreeze/arm64-v8a/adbreeze   # GOOS=android GOARCH=arm64 产物
            │   └── adb/arm64-v8a/adb + *.so      # Termux android-tools arm64
            └── java/com/smanx/adbreeze/
                ├── MainActivity.kt   # 启动编排 + WebView
                ├── ServerService.kt  # 前台服务：子进程生命周期 + 5037/9080 看护
                └── ExecUtil.kt       # linker64 包装执行 + LD_LIBRARY_PATH
```

**启动时序**：

```
① Service.onCreate
② linker64 exec adb start-server ──▶ 轮询 127.0.0.1:5037（≤5s，失败给错误提示）
③ linker64 exec adbreeze（env 注入见 3.4 表）
④ 轮询 127.0.0.1:9080 就绪（≤8s）
⑤ WebView.loadUrl("http://127.0.0.1:9080")
⑥ 通知栏常驻；Activity 可见时前台、退后台服务继续
```

---

## 5. 构建与 CI

本地/CI 一键交叉编译（本机 Go 1.26.3 即可直接验证）：

```bash
# 1. 前端产物（Makefile 已有契约）
cd web && pnpm install && pnpm run build && cd ..

# 2. Go 交叉编译 android/arm64
GOOS=android GOARCH=arm64 CGO_ENABLED=0 \
  go build -ldflags "-s -w -X main.version=$(git describe --tags --always)" \
  -o android/app/src/main/assets/adbreeze/arm64-v8a/adbreeze ./cmd/adbreeze

# 3. 打包 APK（Android Studio / gradlew assembleRelease）
```

- CI（`release.yml` 扩一个 job）：web build → cross compile → `gradlew assembleRelease` → APK 挂 release artifact。
- 签名：开发期 debug key；正式版 release key 走 GitHub Secrets，**不进仓库**。
- ABI：一期只做 `arm64-v8a`；`armeabi-v7a` 与 `amd64`（模拟器）列 Phase 3。

---

## 6. 风险清单 / 真机实测项

| # | 风险 | 验证方式 |
|---|---|---|
| 1 | linker64 包装执行在不同 ROM 的表现差异 | 真机矩阵：Android 10 / 12 / 14 / 15 各一台 |
| 2 | Termux adb 脱离 Termux 环境后能否正常 fork-server（依赖 so 是否带全） | `adb version` / `adb devices` / `adb connect` 三步 |
| 3 | WebView 全链路：WS 投屏 + WebCodecs 硬解 + WebRTC 兜底 + AudioWorklet | 逐功能点检 |
| 4 | `requestFullscreen` 在 WebView 的行为 | 投屏界面点全屏，看是否需要 `onShowCustomView` |
| 5 | Android 14/15 前台服务 specialUse 声明合规 | Play/安装无警告 |
| 6 | 目标设备 Android 11+ 无线调试配对流程（可在开发者选项关「无线调试」再开重连） | 连接/掉线/重连 |
| 7 | 端口被其他 app 占用（9080/5037） | 设置页可改端口（Phase 2） |
| 8 | Android 15+ 是否进一步收紧 exec 策略 | 持续关注 AOSP SELinux 变更 |

---

## 7. 分期实施

- **Phase 1（最小可用，1~2 天）**：壳 + linker64 启动 + adb 前置拉起 + WebView；admin/admin 写死；无设置页。目标：装机能投屏、能终端、能传文件。
- **Phase 2（体验，2~3 天）**：设置页（端口 / 账号密码 / adb 路径 / 是否绑全接口）；通知操作；5037/9080 看护重拉；服务起不来时的日志错误页。
- **Phase 3（增强）**：开机自启（BOOT_COMPLETED，可选）、局域网访问开关、armv7/amd64、fullscreen 优化、release 签名 + CI 自动出包。

## 8. 验收清单

- [ ] 装 APK → 点图标 → 无额外操作出现登录后的 adbreeze 界面
- [ ] `adb connect` 一台无线调试设备，列表刷新正常
- [ ] 投屏 + 反向触控 + 音频转发可用
- [ ] 终端 / 文件上传下载 / 剪贴板 / 截图 / 装 APK 可用
- [ ] 退到桌面 5 分钟后回来，服务与流仍在
- [ ] 杀 adb server（Service 停止再拉起）后自动恢复
- [ ] APK 不含任何源码改动痕迹（主仓库 diff 为空）

---

## 9. 实施落地记录（与设计的差异点）

> 实现见 `android/`（Kotlin 壳）、`scripts/fetch-adb-android.py`（adb 资产拉取）、
> `Makefile`（android-go / android-apk）、`.github/workflows/android.yml`（CI 自动出包）。
>
> **App 形态（v2 调整）**：不做内置 WebView 界面——App 是纯「服务控制台」：
> 配置表单（端口 / 账号 / 密码，存 SharedPreferences）+ 启动/停止按钮 +
> 实时日志（服务端输出经 logListeners 回调到界面）+「打开页面」唤起系统浏览器 +
> 启动状态标记（● 绿=运行中 / 橙=启动中 / 红=未启动）。
> 启动参数与 PC 端一致：不设 `ADBREEZE_HTTPS`（默认 true），HTTP + HTTPS :9443
> 双端口——HTTPS 服务于局域网其他设备浏览器的 WebCodecs 硬解（本机回环本就是
> 安全上下文，无需 HTTPS）。配置经环境变量注入 Go 进程；服务就绪后通知栏显示地址。

1. **adb 资产自动化**：不把 25MB 二进制提交进 git，由
   `python scripts/fetch-adb-android.py [aarch64|x86_64]` 从 Termux 仓库拉取
   android-tools + 依赖 + termux-exec，纯 Python 解析 deb（ar → tar.xz）并按
   ELF DT_NEEDED 算依赖闭包（只带真实需要的 58 个 so；libc/libdl/liblog/libm
   用系统自带）。`make android-go` 时资产缺失会自动触发拉取。
2. **adb server 用 `adb server nodaemon` 前台常驻（最终方案，实测通过）**。
   不能用 `adb start-server`：其内部 fork+execv 拉起 fork-server 子进程，
   Android 10+ 下被 SELinux 拦；LD_PRELOAD 钩子（termux-exec）也救不了——
   它把 execv 重定向到写死的 `/system/bin/linker64`，x86 主机（模拟器）上是
   x86 版 linker，跑不了 arm64 ELF；且 Go 侧裸 syscall exec 根本不受钩子保护。
   `server nodaemon` = is_server + no_daemon 且非 fork-server → 不需要
   ack reply fd、无任何内部 exec，进程由壳的 Service 持有、退出即回收，
   两个平台通吃。实测在 MuMu 模拟器（x86_64 + houdini）上 server 起来后
   `adb devices` 直接发现本机设备。termux-exec 库仍随资产打包备用。
3. **linker64 调用形态 + houdini 兜底**：`ProcessBuilder("/system/bin/linker64",
   binary, args...)` —— bionic linker 直接调用模式，目标程序看到的 argv[0] 是
   自己真实路径。x86_64 模拟器（MuMu 等，首选 ABI 非 arm64）改用 NativeBridge
   自带的 `/system/bin/arm64/linker64` 转译执行 arm64 ELF 及其全部 arm64 so 依赖
   （x86 版 linker64 会报 "is for EM_AARCH64 (183) instead of EM_X86_64 (62)"）。
   已实测：Go 静态 PIE 二进制与 adb + 58 个依赖 so 都能跑通。
4. **Go 原始 syscall exec 不受 LD_PRELOAD 保护**（Go 走 SYS_execve 裸系统调用）：
   Go 服务内部 `EnsureServer` 的 exec adb 在安卓上永远失败——设计上让壳前置
   拉起 adb server 使其不可达分支永不触发；副作用是网页设置里「adb 路径测试」
   在安卓端不可用（已知限制）。已实测：壳前置拉起后 Go 的 Ping 直连 5037，
   设备发现/列表 API 正常（MuMu 上自动发现本机 adbd）。
5. **一期仅 arm64-v8a**：Go 的 `android/amd64` 目标需要 cgo 外部链接
   （纯 Go 不支持），x86_64 依赖 NDK 工具链，列入后续。x86_64 的 adb 资产
   可用 fetch 脚本随时生成（脚本已双 ABI 支持）。
6. **HTTPS 关闭**：`ADBREEZE_HTTPS=false`，WebView 加载 `http://127.0.0.1:9080`
   ——回环地址是浏览器安全上下文，WebCodecs 照常可用，免整套自签 CA。
7. **构建链**：`make android-apk` = web 构建 → Go 交叉编译（**必须
   `-checklinkname=0`**：pion 依赖的 wlynxg/anet 用 go:linkname 访问 net 包
   内部符号，Go 1.23+ 默认拒绝）→ gradlew assembleDebug。CI 见
   `.github/workflows/android.yml`（push 构建检查，ubuntu-latest 自带 Android
   SDK）与 `.github/workflows/release.yml`（**release 统一出包**：tag 触发时
   在 build job 里拉 adb 资产 → assembleRelease → 以
   `adbreeze-android-<版本>.apk`
   随桌面平台一起挂 release 并计入 checksums；版本号经 `-PappVersion` 注入，
   versionCode = x*10000+y*100+z 自动推导）。
