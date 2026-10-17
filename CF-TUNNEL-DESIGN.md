# ADBreeze 外网访问（Cloudflare Tunnel）设计方案

> 状态：设计稿 v2（2026-10-17 修订：采纳「设置驱动的扩展功能」形态）
> 目标读者：本项目维护者
> 关联代码：`cmd/adbreeze/main.go`、`internal/config`、`internal/settings`、`internal/api/router.go`、`web/src/components/Shell.tsx`

---

## 1. 目标与场景

**目标**：让本机启动的 ADBreeze 服务获得一个公网 HTTPS 地址，手机不连同一 Wi-Fi 也能打开管理界面、操控通过 adb 连在本机的安卓设备。

**形态定位（v2 核心变化）**：一个**默认关闭、安装包零增长的扩展功能**——cloudflared 不内置，用户在设置页加载配置（路径 / token / 指向）后一键启动，随时可关。替代 v1 的"命令行 flag 驱动"，理由见 §6 评估。

**典型场景**：

| 场景 | 说明 |
| --- | --- |
| 出门在外 | 人在外面，用移动网络访问家里的 ADBreeze，查看/投屏家里电脑上的设备 |
| 临时演示 | 把地址发给同事，对方无需安装任何东西即可看投屏 |
| 无公网 IP | 家庭宽带 / 公司 NAT 后面拿不到公网 IP，也不想在路由器上做端口映射 |

**非目标**（明确不做）：

- 不做 P2P 穿透——WebRTC 媒体面的连通性是 ICE 的事，隧道只承载 HTTP/WS 信令（见 §5.5）
- 不做多用户 / 多租户——仍是单管理员密码模型
- 不替代本地 HTTPS 自签证书方案——两者互补（见 §5.5）

---

## 2. 方案选型

| 方案 | 做法 | 结论 |
| --- | --- | --- |
| A. 引入 cloudflared Go 库 | `import github.com/cloudflare/cloudflared/...` 把隧道跑在自己进程内 | **否决**。依赖树膨胀（quic-go / grpc / 一堆 transport 库），且用的是上游内部 API，上游一重构就编译失败，维护成本全压在我们这 |
| B. 子进程托管 cloudflared | 运行时 spawn 官方 `cloudflared` 二进制，托管其生命周期 | **推荐**。协议演进零维护；子进程崩溃不影响主服务；Quick / Named 两种模式天然支持；项目已有管理 adb 子进程的成熟模式可循 |
| C. 自研隧道协议 | 自己实现 Cloudflare 的 QUIC/HTTP2 出站协议 | **否决**。不现实 |

选 B 的三个关键理由：

1. **出站连接模型**：cloudflared 只主动向外建连，不需要公网 IP、不需要路由器开端口、不改变本机防火墙暴露面。
2. **进程隔离**：cloudflared 崩溃 / 被墙 / 版本抽风，最多是外网功能不可用，本地 `:9080` 毫发无损。
3. **官方分发**：各平台都有官方二进制，版本升级对我们是透明的——正因如此才不必内置（见 §5.8 体积分析）。

---

## 3. 两种隧道模式

| | Quick Tunnel（默认） | Named Tunnel（可选） |
| --- | --- | --- |
| Cloudflare 账号 | 不需要 | 需要（免费注册即可） |
| 启动命令 | `cloudflared tunnel --url http://127.0.0.1:9080` | `cloudflared tunnel run --token <token>` |
| 公网地址 | 随机 `https://<随机词>.trycloudflare.com`，重启即变 | 固定域名（自己在 CF 后台绑） |
| 前置身份层 | 无 | 可叠加 **Cloudflare Access**（邮箱 OTP / Google 登录），公网入口再加一道 |
| 适用 | 临时外网访问、开箱即用 | 长期固定入口、安全要求高 |

设计取向：**Quick 为主，Named 作为同一套机制的自然延伸**（只差一个 token 配置），不为 Named 单独造 UI。

---

## 4. 整体架构

```
                    公网
浏览器 ──HTTPS──► Cloudflare Edge（边缘终止 TLS，证书受信任）
                         │
                    QUIC/HTTP2 出站连接（cloudflared 主动外连，无需入站端口）
                         │
                    cloudflared 子进程 ──HTTP──► 127.0.0.1:9080（ADBreeze 本机服务）
                         ▲
              internal/tunnel.Manager 托管：启动 / 停止 / 守护重启 / 状态与日志
                         ↑ 配置来自
              settings.json（设置页可改，重启进程仍生效）
```

数据流要点：

- 隧道默认指向 **`http://127.0.0.1:9080`**，不是网卡地址。这意味着外网能力与"服务是否对外监听"**解耦**——用户完全可以把 `-addr` 收紧成 `127.0.0.1:9080`，本地局域网都进不来，但外网隧道照常工作。
- 边缘侧就是受信任证书，浏览器拿到的是**安全上下文**：WebCodecs 可用、无需信任本地自签 CA、cookie（SameSite=Lax、无 Secure 限制）同源正常携带。

---

## 5. 详细设计

### 5.1 配置模型：启动基线 + 设置页存档（两层）

**A. 启动基线**（命令行 > 环境变量 `ADBREEZE_*` > 默认值），只保留无头场景（Docker / Termux / 安卓服务，没有浏览器 UI）必需的最小集：

| 命令行 | 环境变量 | 默认 | 说明 |
| --- | --- | --- | --- |
| `-tunnel` | `ADBREEZE_TUNNEL` | `false` | 启动即开启隧道（无头场景的开关） |
| `-tunnel-token` | `ADBREEZE_TUNNEL_TOKEN` | 空 | 无头场景直接给 token（Named 模式） |

**B. 设置页存档**（`settings.json`，`GET/PUT /api/settings/tunnel`，网页随时改、重启进程仍生效）：

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `binary` | 空 | cloudflared 路径；空 = 自动探测（该值 → 数据目录 `bin/` → 可执行文件同目录 → PATH → 常见安装位置） |
| `service` | 空 | 隧道指向的本地地址；空 = `http://127.0.0.1:<HTTP端口>` |
| `protocol` | 空 | cloudflared 传输协议；空 = 默认（quic），公司网络封 7844/UDP 时填 `http2` |
| `token` | 空 | Named 模式 token（**0600 落盘**，见 §5.4；GET 接口只回脱敏预览，不回全文） |
| `autoRestart` | `true` | 子进程意外退出后自动重启（退避 1s→2s→4s…封顶 30s） |
| `resume` | `false` | **记住上次状态**：开启动作置 `true`、手动「关闭」置 `false`。`true` 时随 ADBreeze 启动自动恢复隧道——即"开启过且没手动关闭（含直接退出/崩溃）→ 自启；从未开启或手动关闭过 → 不自启"（2026-10-17 按用户要求从手动开关 `autoStart` 改为该语义，旧字段一次性迁移） |

**合并语义**（沿用 adb 路径先例：命令行是启动基线，界面改动是用户最新意图）：

- 启动时开启条件：`resume == true` **或** `-tunnel`；
- `resume` 由开启/关闭动作维护（`api.setResume`），**不接受设置页直接勾选**——没有开关，状态即意图；
- 开启参数以存档为准；`token` 存档为空时回退到 `-tunnel-token` 的值（无头场景 UI 从未被访问、存档为空，必须能凭 flag 跑通）；
- 之后用户在设置页保存，即以存档为准覆盖基线。

### 5.2 `internal/tunnel` 包

**Manager 状态机**：

```
disabled ──Start()──► starting ──拿到URL──► running ──Stop()──► stopped
                          │                     │
                          │ 30s 未取到 URL       │ 子进程退出（非用户停止）
                          ▼                     ▼
                        failed ◄──────────── restarting（退避后重来）
```

**Status（API / UI 的数据源，纯运行时态，绝不落盘）**：

```go
type Status struct {
    Running    bool      `json:"running"`
    Mode       string    `json:"mode"`       // "" | "quick" | "named"
    URL        string    `json:"url"`        // quick 模式的外网地址
    Error      string    `json:"error"`      // 最近一次失败原因（成功时为空）
    Restarts   int       `json:"restarts"`   // 自动重启次数
    StartedAt  time.Time `json:"startedAt"`
    BinaryPath string    `json:"binaryPath"` // 实际使用的 cloudflared
    Logs       []string  `json:"logs"`       // 最近 200 行 cloudflared 输出（环形缓冲）
}
```

**核心逻辑**：

- **URL 解析**：逐行读 cloudflared 的 stdout/stderr（quick tunnel 的 banner 在 stderr），正则 `https://[a-z0-9-]+\.trycloudflare\.com` 命中即置 running。上游改 banner 格式时的兜底：30 秒没解析到 URL → 记 error、进程保留运行（用户能从 Logs 里看到现场），不误杀。
- **守护**：`cmd.Wait()` 返回后，若 ctx 未取消且 `autoRestart` 开启 → 退避重启；Quick Tunnel 掉线是常态，不重启等于功能静默死亡。
- **停止**：先 `Signal(os.Interrupt)`，等 3 秒，再 `Kill()` 兜底。Windows 上中断信号对非控制台子进程可能无效，直接走 Kill——quick tunnel 无本地状态，强杀无损；Named 模式的连接状态在 CF 侧，同样无损。
- **token 保密**（安全红线）：token **只通过子进程环境变量 `TUNNEL_TOKEN` 传递，不进 argv**（`ps` 可见 argv）、不进日志、不进 API 响应。
- **配置热更新**：设置页保存配置时若隧道正在运行 → 自动 stop → 用新配置 start，用户无需手动重启（与切换 adb 路径后立即重启 server 的体验一致）。

### 5.3 API

全部挂在现有 `authed` mux 下（继承会话鉴权，未登录一律 401）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/settings/tunnel` | 读配置 + 二进制探测结果：`{ path, resolved, available, version, platform, tokenSet, tokenMask, service, protocol, autoRestart, resume, weakPassword }`。探测 = 解析路径并执行一次 `cloudflared --version`（约几十毫秒，设置页可接受） |
| PUT | `/api/settings/tunnel` | 保存配置并落盘（合并语义：未提供的字段保持原值）；隧道在跑则自动热重启应用新配置。`400`：配置非法（service 不是 http URL 等） |
| POST | `/api/settings/tunnel/install` | 一键安装 cloudflared：后台从官方 GitHub releases 下载当前平台二进制到 `<数据目录>/bin/`。`409`：安装中 / 平台不受支持 / 数据目录未知。立即返回，进度经下一条轮询 |
| GET | `/api/settings/tunnel/install` | 安装状态：`{ state: idle/downloading/verifying/done/failed, progress, downloaded, total, path, version, sha256, error }` |
| GET | `/api/tunnel` | 返回 Status（§5.2） |
| POST | `/api/tunnel/start` | 开启。`409`：探测不到 cloudflared（响应体附安装指引）。默认密码不拦截，仅 WARN 日志 + UI 提醒（见 §5.4-1） |
| POST | `/api/tunnel/stop` | 关闭 |

错误语义沿用项目现有约定（`writeErr` + JSON `{"error": "..."}`），文案说清"怎么修"而非只报现象。

### 5.4 安全设计（本方案的重心）

ADBreeze 的外网暴露面 = **完整的设备控制台**（投屏、shell、文件、装 APK）。这和局域网监听不是一个威胁模型，措施相应升级：

1. **默认密码：警告但不拦截**（2026-10-17 按用户要求从"硬阻断"放宽）。`password == "admin"` 时开启不返回 409：是否暴露公网是用户的显式选择，与项目对局域网"默认密码 + 对外监听只警告不拦截"的哲学一致。保留的提示：start 时服务端记 WARN 日志；UI 在卡片内展示中性提醒（不置灰按钮）；开启前的 confirm 弹窗列明风险。
2. **运行时可开启的姿态变化**：flag 是启动时一次性决定；设置页开启意味着任何已登录会话都能把服务暴露到公网。三道缓解：开启前 confirm 弹窗列明风险；服务端记 WARN 日志（含 URL，**不含 token**）；**顶栏常驻「外网」徽章**（绿色地球图标 + 当前模式），任何页面都看得见"此刻正暴露在公网"，防遗忘。
3. **URL 即门禁**：Quick Tunnel 地址是随机词，不主动分享即不可枚举；但分享出去后，拿到 URL 的人都能到达登录页。最后防线是强密码 + 现有防爆破（连续 5 次失败锁 30s）。
4. **token 落盘策略**：`settings.json` 目前是 `0644` 明文（只存 adb 路径，无敏感字段）。存入 token 后：Store 写盘权限**收紧到 `0600`**（与 `DataDir/tls` 下的 CA 私钥一致）；GET 接口只回脱敏预览（前 4 位 + 后 4 位）；UI 在 token 输入框旁标注"明文保存于本机配置文件"。备选方案是 token 不落盘只存内存（重启重填），评估后否决：Named 用户重启后重填 token 体验差，而 token 本就可从 CF 后台随时吊销重置，0600 落盘与"用户配置目录"的威胁模型匹配。
5. **Named 模式推荐叠加 Cloudflare Access**：在公网入口前加邮箱 OTP / Google 登录，即使密码泄露也有第二道门。Quick Tunnel 无域名 zone 用不了 Access——这是 Named 模式的核心增值，UI 文案里明确引导。

### 5.5 与现有能力的兼容性

| 能力 | 经隧道是否可用 | 说明 |
| --- | --- | --- |
| REST API（设备/文件/APK/进程等） | ✅ | 原样经过，cookie / Bearer 均可 |
| WebSocket `/ws/*`（事件、终端、logcat、ws 视频） | ✅ | CF tunnel 支持 WS 升级 |
| **WebRTC 投屏** | ✅（STUN 已配） | 信令（`POST /api/devices/{serial}/webrtc` + WS）走隧道没问题；媒体是 P2P 不过隧道。默认配公共 STUN（`-webrtc-stun`，Cloudflare + Google，`off` 可关）：两端可打洞时自动回 P2P；对称 NAT 等打不通的场景退化到 WS 视频通道（能过隧道，延迟高一些）。要根治需自建 TURN（`-webrtc-stun turn:...` 覆盖） |
| HTTPS 自签证书 | ✅ 互补 | 走隧道的用户不需要信任本地 CA；边缘即受信任证书，天然安全上下文 |
| CORS | ✅ 无感 | 经隧道域名访问时同源；跨源部署照旧用 `-cors-origins` |
| 自动打开浏览器 | ✅ 不变 | 仍开本地地址；启动日志额外打印外网 URL（若启动即开） |
| Docker 部署 | ✅ | 容器内同样 spawn cloudflared，镜像加一行装 cloudflared 即可（Dockerfile 改动不进 P1 必需） |
| Android APK 内嵌服务端 | ❌ 不支持 | 无官方 Android 版 cloudflared；Termux 内可 `pkg install cloudflared` 手动用。文档注明 |

### 5.6 前端（设置页「外网访问」卡片）

位置：`web/src/components/Shell.tsx` 的 `SettingsPanel`，插在 `TlsSettings` 之后，新增 `TunnelSettings` 组件。卡片分三区：

**状态区**（数据源 `GET /api/tunnel`，开启期间 3s 轮询，关闭后停）：

- 徽章：未开启 / 启动中 / 已连接 / 失败 / 已停止
- Quick 模式显示 URL + 一键复制 + **二维码**（手机相机扫码直达；零依赖编码器，见 §9-6）；Named 模式显示"已绑定固定域名"（具体域名在 CF 侧，本地不感知）
- 自动重启次数 + 运行时长
- 折叠日志面板（`Status.Logs`）

**配置区**（数据源 `GET /api/settings/tunnel`，保存走 PUT）：

- cloudflared 探测态：未安装 → 按 `runtime.GOOS` 显示安装指引（`winget install --id Cloudflare.cloudflared` / `brew install cloudflared` / 官方下载页）+ **「一键安装」按钮**（下载官方二进制到数据目录，进度条 + 完成后显示 SHA256，可随时重跑以更新）；已就绪 → 显示路径与版本
- 路径输入框（留空 = 自动探测，placeholder 显示探测结果）
- token 输入框（密码框 + 已配置时显示脱敏预览；注明明文存本机）
- 高级折叠：service / protocol / autoRestart 勾选
- 自启无开关：按当前运行状态显示一句说明（运行中→"不按关闭直接退出，下次自动恢复"；未运行→"开启一次即记住，直到按关闭"）
- 保存按钮（保存后在跑则自动热重启，toast 说明）

**控制区**：

- 开启 / 停止按钮；开启前 confirm 弹窗（风险告知文案）
- 默认密码时不拦截，仅展示中性提醒（"建议尽快设置强密码"）
- Named 模式提示可绑定 Cloudflare Access

**顶栏徽章**：`Running` 为真时常驻显示（所有页面可见），点击跳转设置页。i18n 补中英文键。

### 5.7 启动流程集成（`main.go`）

```go
// 配置合并：存档优先，token 缺省回退 flag（无头场景存档为空）
tunOpts := mergeTunnelOpts(cfg, st.Get().Tunnel)   // -tunnel / -tunnel-token 仅作基线
tun := tunnel.New(tunOpts)
handler := api.New(auth.New(cfg.Password), version, mgr, mirror, webFS, st, cfg.CORSOrigins, tlsInfo, tun)

// HTTP 先起，再开隧道（隧道要打本地服务）
go httpSrv.ListenAndServe() ...

// 开启条件：上次开启过且没手动关闭（resume），或无头场景传了 -tunnel
if tunOpts.ResumeEnabled() || cfg.Tunnel {
    _ = tun.Start()
    // -tunnel 开启同样记住状态：下次无 flag 也恢复
    if cfg.Tunnel && !tunOpts.ResumeEnabled() {
        tunOpts.Resume = &cfg.Tunnel
        cur := st.Get(); cur.Tunnel = tunOpts; _ = st.Set(cur)
    }
}

<-ctx.Done()
tun.Stop()                          // 先收隧道：退出过程中不再放新请求进公网
httpSrv.Shutdown(shutdownCtx)       // 再关本地服务
if httpsSrv != nil { httpsSrv.Shutdown(shutdownCtx) }
```

两条原则沿用现有哲学：**隧道启动失败不 fatal**（本地服务优先，与 HTTPS 起不来不 fatal 一致）；**退出时先收隧道再关服务**。

> 备注：`api.New` 参数已达 8 个，建议顺势收拢为 `api.Deps` 结构体；如想控制改动面，也可直接追加一个参数，P1 不强求。

### 5.8 cloudflared 二进制分发与体积分析

**实测数据**（cloudflared `2026.10.1`，2026-10-15 发布，2026-10-17 实测；压缩率为 gzip -9，正式安装器用 LZMA 大体同量级）：

| 平台 | cloudflared 原始体积 | 压缩后 | 官方安装器 |
| --- | --- | --- | --- |
| Windows amd64 | 52.8 MB | 17.9 MB（34%） | MSI 18.8 MB |
| Windows 386 | 36.3 MB | — | MSI 19.0 MB |
| Linux amd64 | 38.4 MB | — | deb 19.0 MB |
| Linux arm64 | 35.9 MB | — | deb 17.4 MB |
| macOS arm64 | 37.6 MB | — | pkg 18.9 MB |
| macOS amd64 | ~37 MB | — | pkg 20.8 MB |

对照：ADBreeze 自身 Windows amd64 产物 **13.4 MB**（含 941 KB 前端），gzip 后 5.7 MB。cloudflared 单体积约为 ADBreeze 的 **4 倍**，且随版本逐年上涨（2024 年约 35 MB → 2026 年 52.8 MB）。

**分发决策：不内置，安装包 +0**。四种方式的代价对比：

| 方式 | 安装包传输增量 | 落盘增量 | 评价 |
| --- | --- | --- | --- |
| ① 外部依赖（**本方案**）：用户 winget / brew 自装，设置页探测 | **+0** | 0 | 安装包不变；多一步用户操作，MSI/pkg 约 19 MB 但由官方渠道分发、可复用 |
| ② 随包附带：打包脚本按平台放对应二进制，压缩分发 | +约 18 MB | +36~53 MB | 开箱即用；每发版要同步更新附带二进制，多一个供应链维护点 |
| ③ go:embed 进单文件 exe | 压缩渠道 +18 MB；裸 exe 分发 +52.8 MB | 同 ② + 每次启动解压落盘 | 单文件方便但 exe 膨胀到 66 MB，不如旁边放一个文件 |
| ④ 首次使用按需下载 | **+0** | 首次开隧道时 +36~53 MB | 体验换体积；需固定下载源 + SHA256 校验 |

方式 ④（设置页一键下载到数据目录 `bin/` + 校验）**已实现**（2026-10-17，见 §9）：安装按钮在设置页「外网访问」卡片的 cloudflared 路径行旁；下载 URL 由服务端按 `GOOS/GOARCH` 拼装官方 release 资产（绝不接受调用方指定 URL，否则本端点会成为任意文件下载代理），落点 `<数据目录>/bin/`（自动探测链第一站），macOS 资产为 `.tgz` 需解包；安装后 `--version` 冒烟 + 界面展示 SHA256。注意 Cloudflare 未随 release 发布校验和文件，故信任模型与 winget / brew 一致（固定官方仓库 + HTTPS），不做外部 checksum 比对。方式 ② 仍仅适合离线/内网分发场景。Android 无官方二进制，三种方式都不增加 APK 体积。

### 5.9 测试计划

| 层 | 内容 |
| --- | --- |
| 单元 | URL 解析（不同 banner 格式 / 无 URL / 提前退出）；quick / named 参数构造；二进制解析顺序；退避序列；**token 不出现在 argv / 日志 / API 响应**（断言）；token 脱敏函数 |
| 状态机 | 启停；意外退出自动重启；ctx 取消后不重启；Stop 幂等；保存配置后热重启 |
| settings | 存档缺字段按默认值（向后兼容旧 settings.json）；写盘后文件权限为 `0600`；坏存档不阻断启动 |
| config | env 解析、resume 判定、token 基线回退（存档空 + flag 有值 → 用 flag） |
| 集成 | stub 假 cloudflared（打印 banner 后挂起的测试小程序）跑通 start → URL → stop 全链路 |
| 安装器 | httptest 全链路（下载→落盘→SHA256→冒烟注入）、资产映射（含 windows/arm64、android 不支持）、tgz 解包（含诱饵成员）、超限限流、并发拒绝、服务端错误、无数据目录 |
| 前端 | 无测试框架（项目现状），人工验证清单：未安装引导、默认密码可正常开启（仅提醒）、保存后热重启、顶栏徽章、URL 复制、日志展示、跨页面刷新状态恢复 |

### 5.10 实施分期

| 阶段 | 内容 | 验收 |
| --- | --- | --- |
| **P1 核心** | `internal/tunnel` 包 + `Settings.TunnelConfig` 扩展 + `settings/tunnel` 与 `tunnel` 两组 API + main.go 接线（resume / `-tunnel` 双入口） | `adbreeze -tunnel` 无头启动即通；curl 可保存配置、开关隧道 |
| **P2 UI 控制** | 设置页「外网访问」卡片（配置/控制/状态三区）+ 顶栏徽章 + i18n | 网页里可配可开可关可看日志，默认密码可开启（仅提醒），未安装正确引导 |
| **P3 加固（可选，全部已完成 2026-10-17）** | ✅ 一键下载 cloudflared、✅ Named + Access 文档、✅ WebRTC STUN 默认配置、✅ URL 二维码 | 一键安装真实跑通（官方源下载→冒烟→真隧道连通）；STUN 默认公共服务器、`-webrtc-stun` 可覆盖；二维码零依赖编码器经 Python 参考库交叉验证；Named/Access 完整步骤见 README |

---

## 6. 形态评估：设置驱动的扩展功能

**结论：采纳。** 该形态与项目既有范式同构，成本主要在一处（token 落盘），可控。

**为什么契合**：

1. **有成熟先例可循**。adb 路径就是"设置里配 → 存档 → 立即生效"的完整链路（`GET/POST /api/settings/adb` + `settings.json`）。tunnel 配置走同一条路，用户认知零成本，后端复用 `settings.Store` 的 JSON 存取与向后兼容机制。
2. **安装包 +0，普通用户完全无感**。功能代码在二进制里（KB 级），cloudflared 不内置——正是 §5.8 实测数据支持的选择。比 flag 门控更彻底：命令行都不用碰。
3. **零重启生效**。改 token / 路径 / 协议后无需重启进程，保存即热重启隧道。flag 方案改任何参数都要重启整个服务。
4. **安全可控性更好**。开启确认 + 随时可关 + 顶栏暴露徽章，用户对"此刻是否暴露在公网"始终有确定答案——这是 flag 方案给不了的（flag 开启后没有任何常驻可见的状态指示）。
5. **跨平台引导自然下沉到 UI**。按 `runtime.GOOS` 展示 winget / brew / 手动路径指引；flag 方案只能在启动日志里泛泛而谈。

**成本与风险（诚实清单）**：

| # | 问题 | 对策 |
| --- | --- | --- |
| 1 | `settings.json` 首次出现敏感凭证（token），现为 0644 明文 | 写盘收紧 0600；GET 只回脱敏预览；UI 标注明文存储。见 §5.4-4 |
| 2 | "运行时可开启"意味着任何已登录会话都能暴露服务到公网 | 确认弹窗 + WARN 日志 + 默认密码提醒（不拦截）+ 顶栏常驻徽章。见 §5.4-2 |
| 3 | 配置与运行时状态易混淆（若把 running/url 存进存档，重启后 UI 假显示"已连接"） | 严格分层：存档只存配置与期望（§5.1-B），Status 纯运行时态（§5.2），GET /api/tunnel 单独取 |
| 4 | 重启后是否自动恢复开启？自动恢复 = 隐式持续暴露 | **按用户要求（2026-10-17）改为"记住上次状态"**：开启过且没手动关闭 → 恢复；从未开启或手动关闭过 → 不恢复。WARN 日志 + 顶栏徽章 + URL 随机性兜底。见 §5.1-B |
| 5 | 配置项比 adb 路径多，UI 复杂度上升 | 三区分组（状态/配置/控制）+ 高级项折叠；参考 TlsSettings 体量，可控 |
| 6 | 存档在用户配置目录，若被同步盘（OneDrive 等）重定向会外泄 token | 0600 + UI 标注；接受残余风险（token 可随时在 CF 后台吊销） |

**配套决策（已定，评审可推翻）**：

1. token 落盘 + `settings.json` 0600（而非"不落盘只存内存"）——理由见 §5.4-4；
2. ~~`autoStart` 默认关，公网暴露必须显式~~ → 已按用户要求演化为"记住上次状态"（resume）：状态即意图，无开关；
3. flag 只保留 `-tunnel` / `-tunnel-token` 最小集服务无头场景，其余配置全沉到设置页。

**对工作量的影响**：相对 v1 的 flag 方案，P1 多出 `Settings.TunnelConfig` 扩展、两个 settings API、token 脱敏与权限处理；P2 UI 从"纯开关"变为"配置 + 控制 + 状态"三区。分期结构不变（§5.10）。

---

## 7. 影响面与回滚

**新增**：`internal/tunnel/`（manager + 状态 + 解析 + 测试）、`internal/api/tunnel.go`（settings + 控制两组端点）、前端 `TunnelSettings` 组件 + 顶栏徽章 + i18n 键。

**修改**：`internal/settings/settings.go`（`TunnelConfig` 字段 + 写盘 0600）、`internal/config/config.go`（`-tunnel` / `-tunnel-token`）、`cmd/adbreeze/main.go`（配置合并 + 接线）、`internal/api/router.go`（路由 + 注入 Manager）、`web/src/components/Shell.tsx`（设置卡片 + 徽章）。

**回滚**：删掉 flag 与设置卡片即回到现状。无数据迁移（`settings.json` 新增字段对旧存档按零值兼容）、无外部服务强依赖（不开隧道时完全不碰 cloudflared）。

---

## 8. 风险与开放问题

| # | 风险 | 对策 |
| --- | --- | --- |
| 1 | 上游改 banner 格式导致 URL 解析失败 | 宽容正则 + 30s 超时只记 error 不杀进程，Logs 供排查 |
| 2 | Windows 上 `Signal(Interrupt)` 对 cloudflared 无效 | Kill 兜底；quick tunnel 无状态，无损 |
| 3 | 公司/校园网封 QUIC（7844/UDP） | 设置页 protocol 切 `http2`；是否做"自动重试 http2"留待真实反馈 |
| 4 | 外网 WebRTC 投屏建连失败 | STUN 已默认配置（两端可打洞即 P2P）；WS 视频通道兜底；对称 NAT 需自建 TURN |
| 5 | cloudflared 未安装 | 设置页按 OS 引导 + 手动路径 + 一键安装 |
| 6 | 随机 URL 被分享后的暴力尝试 | 默认密码提醒（不拦截）+ 现有登录锁定（5 次锁 30s）；Named + Access 是正解 |

**开放问题（评审结论已回填）**：

1. `settings.json` 收紧 0600 是否认可？→ **已实施**（写盘 0600 + 旧文件 chmod 收敛）。
2. `api.New` 是否顺手收拢成 `Deps` 结构体？→ **已实施**（P3-a 加 STUN 参数时一并收拢，见 `api.Deps`）。
3. Quick Tunnel 的随机 URL 是否要在 UI 里生成二维码？→ **已实施**（P3-b，零依赖编码器 + Python 参考库交叉验证 + DOM 端到端比对）。
4. P3 的"一键下载 cloudflared"是否要做？→ **已实施**（P3 一键安装，真实下载冒烟通过）。

---

## 9. 实施记录（2026-10-17，P1 + P2 + P3 + P4 全部完成）

**落地文件**：

| 层 | 文件 |
| --- | --- |
| 新增 | `internal/tunnel/tunnel.go`（Manager / 状态机 / URL 解析 / 守护 / 环形日志）、`internal/tunnel/resolve.go`（二进制解析 / token 脱敏 / 版本探测）、`internal/tunnel/install.go`（一键安装：资产映射 / 下载 / 解包 / 冒烟）、`internal/tunnel/tunnel_test.go`、`internal/tunnel/install_test.go`、`internal/api/tunnel.go`、`internal/settings/settings_test.go`、`web/src/qrcode.ts`（零依赖 QR 编码器）、`web/src/components/QrCode.tsx`、`web/scripts/verify-qrcode.{mjs,py}`（QR 矩阵交叉验证工具）、`web/scripts/qr-dom-check.mjs`（DOM 端到端比对） |
| 修改 | `internal/settings/settings.go`（`TunnelConfig` + 写盘 0600）、`internal/config/config.go`（`-tunnel` / `-tunnel-token` / `-webrtc-stun` / `DefaultPassword`）、`internal/auth/auth.go`（`WeakPassword()`）、`internal/webrtc/session.go`（`NewManager(stunCSV)` + ICE 服务器接入 + `ParseICEServers`）、`internal/webrtc/stun_test.go`、`internal/api/router.go`（7 条路由 + `Deps` 收拢）、`cmd/adbreeze/main.go`（配置合并 + 启停接线）、`web/src/api.ts`、`web/src/i18n.ts`、`web/src/components/Shell.tsx`（`TunnelSettings` + 顶栏徽章 + 二维码）、`web/src/components/Icon.tsx`（globe）、`web/src/styles.css` |

**与设计的偏差**（实施中收得更好或按用户要求调整，特此记录）：

1. **PUT `/api/settings/tunnel` 用合并语义**：请求体全部字段为指针，nil = 保持原值。设计原文是"保存并落盘"（易被理解为全量替换）；冒烟测试发现只传 token 的调用方会把 `binary` 抹掉，故改为合并——防止部分更新清空其他字段。
2. **settings 响应增加 `weakPassword` 字段**：设计里默认密码门控只有 start 端点的 409；为实现"UI 提醒"回显该标志（后端仍以 `auth.WeakPassword()` 为唯一事实源，UI 只做展示）。
3. **默认密码从"硬阻断"放宽为"警告"**（2026-10-17 用户要求）：start 不再对默认密码返回 409，按钮不置灰；与项目对局域网"默认密码 + 对外监听只警告不拦截"的哲学对齐。保留 start 时的 WARN 日志、卡片内中性提醒、开启前 confirm 弹窗三道提示。
4. **一键安装（§5.8 方式④）落地时补了 `platform` 字段**：settings 响应回显服务端 `GOOS/GOARCH`——浏览器所在平台未必等于服务端平台（手机浏览器配 PC 后端是常态），安装提示必须说清"将下载哪个平台的版本"。
5. **P3-a 借机把 `api.New` 收拢为 `Deps` 结构体**（开放问题 2）：加 STUN 参数时位置参数将达 10 个，遂一并收拢；调用方仅 main.go 一处。
6. **P3-b 二维码编码器**：零依赖自研（byte 模式 + ECC M + 版本 1-10），矩阵用 Python `qrcode` 库交叉验证（6 组文本全过，含 UTF-8 中文），再做浏览器 DOM 端到端比对（UI 渲染 = 编码器输出 = 参考实现，全链路闭环）。过程中发现并修复三个真 bug：RS 生成多项式升降序用反、格式位序是 MSB 在前（bit14 在探测图案侧）、copy-2 横条从左向右而非从右向左。
7. **自启语义从手动开关改为"记住上次状态"**（2026-10-17 用户要求，§5.1-B/§6 决策 2 随之改写）：`autoStart` 勾选 → `resume` 状态位——开启动作（含 `-tunnel`）置 true、手动「关闭」置 false，启动时据此决定是否恢复；旧 `autoStart` 存档一次性迁移。UI 去掉勾选，改为按运行状态显示一句说明。风险评估：这相对"公网暴露必须显式"是退让，但状态即意图（开过没关 = 想让它在跑），保留 WARN 日志、顶栏徽章、URL 随机性三重可见性；彻底关闭路径 = 开一次关一次。

**验证**：

- 单测：`go test ./...` 全绿（tunnel 包 22 项 + webrtc 新增 STUN 解析 9 例；"替身 cloudflared"由测试二进制自身冒充）
- 冒烟（桩 cloudflared，隔离 APPDATA）：401 鉴权、无二进制 409、保存即探测、start→running+URL、部分 PUT 保留 binary、热重启后 named 模式、stop→stopped；默认密码实例可正常开启（返回状态而非 409）
- **resume 三轮重启实验（隔离 APPDATA）**：① 全新存档启动 → idle（不盲目自启）；手动开启 → running 且存档 `resume:true`；② 不手动关闭直接杀进程 → 重启自动 running（日志"按上次运行状态（未手动关闭）开启"）；③ 手动「关闭」→ stopped 且 `resume:false` → 杀进程重启 → idle（不恢复）。UI 侧：设置页 resume 说明文案按运行态正确切换，无 autoStart 残留
- **真实端到端（2026-10-17，官方 GitHub 源）**：一键安装真实下载 52.8MB → `--version` 冒烟得 `cloudflared version 2026.10.1` → 落盘 `<数据目录>/bin/cloudflared.exe` → 探测自动发现 → 真二进制启动隧道 → 拿到真实公网地址 `https://carpet-services-etc-lines.trycloudflare.com` → 经该地址访问：未登录 API 401、登录页外壳 200、带 token API 200 → stop 正常
- UI（真实浏览器走查）：运行中顶栏「外网访问中」徽章 + 状态卡（已连接/Quick/URL/复制/运行时长/日志）+ URL 二维码（29×29 矩阵与参考实现等价）；一键安装按钮 + 平台提示 + 安装后「安装位置 + SHA256」展示；默认密码实例开启按钮可点 + 中性提醒 + 徽章不显示
- 构建：`go build ./...` + `pnpm build`（tsc+vite）+ 重出 `bin/adbreeze.exe`（dist 走 go:embed）

**P3 + P4 全部完成**：一键安装 cloudflared、Named + Cloudflare Access 文档（README）、WebRTC STUN 默认配置（`-webrtc-stun` 可覆盖/关闭）、URL 二维码、自启"记住上次状态"（resume，含旧 autoStart 迁移）。无遗留待办。
