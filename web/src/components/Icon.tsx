import type { ButtonHTMLAttributes, ReactNode } from 'react'

// 面板 Tab 图标：内联 SVG（stroke 风格，24×24 视口，currentColor 继承按钮颜色）。
//
// 为什么不用 public/ 下的 PNG/SVG 文件 + <img>：
//   - 无法跟随 currentColor 变色，active 态要变主色就得再备一套主色图标；
//   - 每个图标一次额外请求，11 个 Tab 就是 11 个请求。
// 图标是纯装饰：aria-hidden，可读名由按钮的 aria-label 提供。
const ICONS: Record<string, ReactNode> = {
  // 概览：手机（设备信息）
  overview: <><rect x="7" y="2" width="10" height="20" rx="2.5" /><path d="M11 18.5h2" /></>,
  // 投屏：显示器
  mirror: <><rect x="2" y="4" width="20" height="14" rx="2.5" /><path d="M8.5 21h7M12 18v3" /></>,
  // 文件：文件夹
  files: <><path d="M4 7a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z" /></>,
  // 应用：九宫格
  apps: <><rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" /><rect x="3" y="14" width="7" height="7" rx="1.5" /><rect x="14" y="14" width="7" height="7" rx="1.5" /></>,
  // 进程：列表
  process: <><path d="M8.5 6h12M8.5 12h12M8.5 18h12M3.5 6h.01M3.5 12h.01M3.5 18h.01" /></>,
  // 性能：折线图
  perf: <><path d="M3.5 3.5v17h17" /><path d="M7.5 14.5l3.5-3.5 3 3 5-5.5" /></>,
  // 布局：带侧栏的框
  layout: <><rect x="3" y="3.5" width="18" height="17" rx="2.5" /><path d="M3 9.5h18M9.5 21V9.5" /></>,
  // 日志：文档文本
  logcat: <><path d="M14 3.5H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8.5z" /><path d="M14 3.5v5h5M9 13h6M9 16.5h6" /></>,
  // 终端：>_
  term: <><path d="M4.5 17l6-5-6-5" /><path d="M12.5 19h7" /></>,
  // 工具：扳手
  tools: <><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" /></>,
  // 设置：齿轮
  settings: <><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" /><circle cx="12" cy="12" r="3" /></>,

  // GitHub 仓库外链（顶栏）：官方标记是填充路径，见下方 FILLED
  github: <><path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.733 18.7 3.733 18.7c-1.087.744.084.729.084.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12z" /></>,
  // 退出登录（顶栏）：门 + 向右外指箭头（log-out 通用标记）
  logout: <><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" /><polyline points="16 17 21 12 16 7" /><line x1="21" y1="12" x2="9" y2="12" /></>,

  // ---------- 投屏工具栏 ----------
  // 返回 / 主页 / 菜单 / 通知 / 电源（与 controller 的 op 名对应，直接 name={op}）
  back: <><path d="M19 12H5" /><path d="M12 19l-7-7 7-7" /></>,
  home: <><path d="M3 9.5l9-7 9 7V20a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" /><path d="M9 22v-10h6v10" /></>,
  menu: <><path d="M4 6h16M4 12h16M4 18h16" /></>,
  notify: <><path d="M18 8a6 6 0 1 0-12 0c0 7-3 9-3 9h18s-3-2-3-9" /><path d="M13.7 21a2 2 0 0 1-3.4 0" /></>,
  power: <><path d="M12 2v10" /><path d="M18.4 6.6a9 9 0 1 1-12.8 0" /></>,
  // 旋转画面（顺时针循环）
  rotate: <><polyline points="21 4 21 10 15 10" /><polyline points="3 20 3 14 9 14" /><path d="M3.5 9a9 9 0 0 1 14.9-3.4L21 8" /><path d="M20.5 15a9 9 0 0 1-14.9 3.4L3 16" /></>,
  // 取剪贴板（设备→本机）：复制
  copy: <><rect x="9" y="9" width="13" height="13" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></>,
  // 粘贴到设备（本机→设备）：剪贴板
  clipboard: <><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2" /><rect x="8" y="2" width="8" height="4" rx="1" /></>,
  // 声音 / 静音
  speaker: <><path d="M11 5L6 9H2v6h4l5 4z" /><path d="M15.5 8.5a5 5 0 0 1 0 7" /><path d="M18.5 5.5a9 9 0 0 1 0 13" /></>,
  'speaker-off': <><path d="M11 5L6 9H2v6h4l5 4z" /><path d="M22 9l-6 6" /><path d="M16 9l6 6" /></>,
  // 全屏（四角括号）：进入 / 退出
  maximize: <><path d="M8 3H5a2 2 0 0 0-2 2v3" /><path d="M21 8V5a2 2 0 0 0-2-2h-3" /><path d="M3 16v3a2 2 0 0 0 2 2h3" /><path d="M16 21h3a2 2 0 0 0 2-2v-3" /></>,
  minimize: <><path d="M8 3v3a2 2 0 0 1-2 2H3" /><path d="M21 8h-3a2 2 0 0 1-2-2V3" /><path d="M16 21v-3a2 2 0 0 1 2-2h3" /><path d="M3 16h3a2 2 0 0 1 2 2v3" /></>,
  // 最大化窗口（对角箭头）：进入 / 退出
  expand: <><path d="M15 3h6v6" /><path d="M9 21H3v-6" /><path d="M21 3l-7 7" /><path d="M3 21l7-7" /></>,
  collapse: <><path d="M4 14h6v6" /><path d="M20 10h-6V4" /><path d="M14 10l7-7" /><path d="M3 21l7-7" /></>,
  // 开始 / 停止
  play: <><path d="M7 4.5v15l13-7.5z" /></>,
  stop: <><rect x="5" y="5" width="14" height="14" rx="2" /></>,
}

// 实心填充图标（GitHub 官方标记是 fill 路径，描边渲染会糊成一团）
const FILLED = new Set(['github'])

export function Icon({ name, size = 18 }: { name: string; size?: number }) {
  const body = ICONS[name]
  if (!body) return null
  const filled = FILLED.has(name)
  return (
    <svg
      className="ico"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill={filled ? 'currentColor' : 'none'}
      stroke={filled ? 'none' : 'currentColor'}
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {body}
    </svg>
  )
}

// 图标按钮：默认只显图标，悬停/聚焦出名称气泡（与顶栏 Tab 同一套 .has-tip 样式）。
// tip 同时用作 aria-label；长说明也放 tip 里（气泡限宽 240px 自动换行）。
export function IconButton({ tip, className = '', children, ...rest }: {
  tip: string
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button className={`has-tip ${className}`} data-tip={tip} aria-label={tip} {...rest}>
      {children}
    </button>
  )
}
