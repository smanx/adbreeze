# screenshots/

关于页截图目录。

每个功能大项一张图（共 6 张），显示在对应板块的大截图位。把 PNG 放进本目录、
按下面命名，构建时 Vite 拷进 `dist/screenshots/` 自动生效；文件缺失时该板块
显示内置占位图，无需改代码。

建议 16:10 比例、宽度 ≥ 1200px（PC 界面全宽截图裁到 16:10 即可）。

| 文件名 | 板块 | 覆盖功能 |
|---|---|---|
| mirror.png | 投屏与交互 | 实时投屏、音频转发、反向触控、多观众 |
| term.png | 命令与控制 | 交互终端、一次性命令、批量命令、进程管理 |
| files.png | 文件与应用 | 文件管理、应用管理、APK 安装、剪贴板互通 |
| monitor.png | 监控与诊断 | 设备概览、性能监控、Logcat、UI 层级分析 |
| network.png | 网络与访问 | Cloudflare Tunnel、Named Tunnel、WebRTC、设备接入 |
| infra.png | 平台与可靠性 | 单文件交付、HTTPS/证书、鉴权、PWA、自愈、双语 |
