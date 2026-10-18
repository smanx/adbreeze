// 关于页（/#/about）：项目功能全景介绍，独立路由、无需登录。
// 双语复用全局 i18n 词典（跟随控制台右上角的语言切换 / localStorage 偏好）。
// 纯静态内容：不请求任何 API，登录前后均可访问。
//
// 版式：每个功能大项一个「截图 + 功能点」的横向板块——
//   截图（16:10，cover 裁切）居一侧，该大项下的所有功能点排在另一侧，
//   左右交替（zigzag）。悬停截图出现「点击放大」提示，点击后灯箱查看原图。
//
// 截图替换：把图片放进 web/screenshots/ 并按 Section.shot 命名
// （如 mirror.png），site 构建（pnpm run build:site）后自动显示；文件缺失时
// 显示 CSS 占位（img onError 回退）。主应用构建不包含截图。
import { useEffect, useRef, useState } from 'react'
import { toggleLang, useT } from '../i18n'
import { Icon } from './Icon'

/** 每个大项的定义：截图文件名 + 功能点列表（i18n 键） */
const SECTIONS: { icon: string; shot: string; titleKey: string; items: string[] }[] = [
  {
    icon: 'mirror', shot: 'mirror', titleKey: 'about.secMirror',
    items: ['about.fMirror', 'about.fAudio', 'about.fControl', 'about.fMultiView'],
  },
  {
    icon: 'term', shot: 'term', titleKey: 'about.secControl',
    items: ['about.fTerm', 'about.fShell', 'about.fBatch', 'about.fProcess'],
  },
  {
    icon: 'files', shot: 'files', titleKey: 'about.secFiles',
    items: ['about.fFiles', 'about.fApps', 'about.fApk', 'about.fClip'],
  },
  {
    icon: 'overview', shot: 'monitor', titleKey: 'about.secMonitor',
    items: ['about.fOverview', 'about.fPerf', 'about.fLogcat', 'about.fLayout'],
  },
  {
    icon: 'globe', shot: 'network', titleKey: 'about.secNet',
    items: ['about.fTunnel', 'about.fTunnelNamed', 'about.fRemote', 'about.fDevices'],
  },
  {
    icon: 'settings', shot: 'infra', titleKey: 'about.secInfra',
    items: ['about.fSingle', 'about.fHttps', 'about.fAuth', 'about.fPwa', 'about.fSelfHeal', 'about.fI18n'],
  },
]

export default function AboutPage() {
  const t = useT()
  // Docker 一键运行：复制到剪贴板（与 README 的 Docker 节命令一致）
  const [copied, setCopied] = useState(false)
  // 背景图加载失败（如主应用构建未打包该文件）时隐藏背景层，回退纯渐变/网格
  const [bgErr, setBgErr] = useState(false)
  // 控制流程动效：滚动进入视口后加 .play 一次性触发入场动画
  const flowRef = useRef<HTMLElement | null>(null)
  useEffect(() => {
    const el = flowRef.current
    if (!el || typeof IntersectionObserver === 'undefined') {
      flowRef.current?.classList.add('play')
      return
    }
    const io = new IntersectionObserver(entries => {
      for (const e of entries) {
        if (e.isIntersecting) { el.classList.add('play'); io.disconnect() }
      }
    }, { threshold: 0.25 })
    io.observe(el)
    return () => io.disconnect()
  }, [])
  const DOCKER_CMD = 'docker run -d --name adbreeze -p 9080:9080 -p 9443:9443 -v adbreeze-config:/config ghcr.io/smanx/adbreeze:latest'
  const copyDocker = async () => {
    try {
      await navigator.clipboard.writeText(DOCKER_CMD)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch { /* 剪贴板不可用（非安全上下文等）：静默 */ }
  }

  return (
    <div className="about-page">
      {/* 背景装饰层：照片底图 + 极光渐变 + 网格 */}
      {/* 背景图与截图同规则：引用 ./screenshots/about-bg.png（相对文档解析，不被 Vite
          打包），site 构建由 copy-screenshots 拷入、dev 由 vite 静态服务、主应用生产
          构建文件不存在 → onError 隐藏背景层，仅剩极光/网格（与截图占位同理） */}
      <div className="about-bg" aria-hidden="true">
        {!bgErr && (
          <div
            className="about-bg-photo"
            style={{ backgroundImage: 'url(./screenshots/about-bg.png)' }}
            onError={() => setBgErr(true)}
          />
        )}
        <div className="about-bg-aurora" />
        <div className="about-bg-grid" />
      </div>

      <header className="about-topbar">
        <span className="brand">ADBreeze</span>
        <span className="about-features-badge">Features</span>
        <span className="spacer" />
        <a
          className="about-gh-link"
          href="https://github.com/smanx/adbreeze"
          target="_blank"
          rel="noopener noreferrer"
          title={t('about.ghLink')}
          aria-label={t('about.ghLink')}
        >
          <Icon name="github" size={20} />
        </a>
        <button className="btn tiny ghost" onClick={() => toggleLang()} title={t('topbar.langTip')}>
          {t('topbar.langBtn')}
        </button>
      </header>

      <main className="about-main">
        {/* Hero：大标题 + 渐变字 + 动效 */}
        <section className="about-hero">
          <div className="about-hero-logo"><Icon name="mirror" size={44} /></div>
          <h1 className="about-hero-title">{t('about.heroTitle')}</h1>
          <p className="about-tagline">{t('about.heroTagline')}</p>
          <div className="about-badges">
            <span className="about-badge">Windows</span>
            <span className="about-badge">macOS</span>
            <span className="about-badge">Linux</span>
            <span className="about-badge">Docker</span>
            <span className="about-badge">Android</span>
            <span className="about-badge glow">{t('about.badgeSingleBinary')}</span>
            <span className="about-badge glow">{t('about.badgeWebOnly')}</span>
          </div>
          <div className="about-cta-row">
            <a
              className="about-cta"
              href="https://github.com/smanx/adbreeze/releases"
              target="_blank"
              rel="noopener noreferrer"
            >
              <Icon name="overview" size={17} /> {t('about.download')}
            </a>
            <button className="about-cta about-cta-ghost" onClick={() => void copyDocker()}>
              <Icon name="copy" size={17} /> {copied ? t('about.dockerCopied') : t('about.dockerRun')}
            </button>
          </div>
          {/* Docker 复制成功的命令回显 */}
          <div className={'about-docker-cmd' + (copied ? ' show' : '')}>
            <code>$ {DOCKER_CMD}</code>
          </div>
        </section>

        {/* 控制流程：两种场景的图形化链路（图标 + 短标签，避免大段文字） */}
        <section ref={flowRef} className="about-flow">
          <h2><Icon name="globe" size={20} /> {t('about.secFlow')}</h2>

          {/* 场景一：被控端与服务端同在一台安卓手机（APK 自身即服务端） */}
          <div className="about-flow-scene">
            <div className="about-flow-scene-title fx" style={{ '--i': 0 } as React.CSSProperties}>
              <Icon name="overview" size={16} /> {t('about.flowSame')}
            </div>
            <div className="about-flow-chain">
              {/* 被控端与服务端同机：两个节点包在同一个「手机」框里 */}
              <div className="about-flow-group fx" style={{ '--i': 1 } as React.CSSProperties}>
                <span className="about-flow-group-tag">{t('about.flowOnePhone')}</span>
                <div className="about-flow-group-inner">
                  <FlowNode icon="overview" label={t('about.flowPhone')} sub={t('about.flowDevice')} />
                  <FlowArrow double />
                  <FlowNode icon="overview" label={t('about.flowPhoneApk')} sub={t('about.flowServer')} accent />
                </div>
              </div>
              <FlowConnNote label={t('about.flowHttp')} noteTop={t('about.flowLan')} noteBottom={t('about.flowCf')} />
              <FlowController label={t('about.flowBrowser')} />
            </div>
            <p className="about-flow-note fx" style={{ '--i': 4 } as React.CSSProperties}>{t('about.flowSameNote')}</p>
          </div>

          {/* 场景二：被控端与服务端分离（服务端可跑在电脑 / Docker / 另一台安卓上） */}
          <div className="about-flow-scene">
            <div className="about-flow-scene-title fx" style={{ '--i': 0 } as React.CSSProperties}>
              <Icon name="laptop" size={16} /> {t('about.flowSplit')}
            </div>
            <div className="about-flow-split">
              <div className="about-flow-devices fx" style={{ '--i': 1 } as React.CSSProperties}>
                <FlowNode icon="overview" label={t('about.flowDevPhone')} sub={t('about.flowDevice')} />
                <FlowNode icon="overview" label={t('about.flowDevTv')} sub={t('about.flowDevice')} />
                <FlowNode icon="overview" label={t('about.flowDevMore')} sub={t('about.flowDevice')} />
              </div>
              <div className="about-flow-split-mid fx-arrow" style={{ '--i': 2 } as React.CSSProperties}>
                <FlowArrow double />
                <span className="about-flow-conn">{t('about.flowUsbWifi')}</span>
                <FlowArrow double />
              </div>
              <div className="about-flow-servers fx" style={{ '--i': 3 } as React.CSSProperties}>
                <FlowNode icon="laptop" label="Windows" sub={t('about.flowServer')} accent />
                <FlowNode icon="laptop" label="macOS / Linux" sub={t('about.flowServer')} accent />
                <FlowNode icon="docker" label="Docker" sub={t('about.flowServer')} accent />
                <FlowNode icon="overview" label={t('about.flowServerAndroid')} sub={t('about.flowServer')} accent />
              </div>
              <FlowConnNote label={t('about.flowHttp')} noteTop={t('about.flowLan')} noteBottom={t('about.flowCf')} />
              <FlowController label={t('about.flowBrowserAny')} />
            </div>
            <p className="about-flow-note fx" style={{ '--i': 4 } as React.CSSProperties}>{t('about.flowSplitNote')}</p>
          </div>
        </section>

        {/* 六大功能板块：一大图 + 功能点清单，左右交替 */}
        {SECTIONS.map((sec, i) => (
          <FeatureSection key={sec.shot} icon={sec.icon} shot={sec.shot}
            title={t(sec.titleKey as never)} flip={i % 2 === 1}>
            {sec.items.map(key => (
              <FeatureItem key={key} k={key} />
            ))}
          </FeatureSection>
        ))}

        {/* Android 端说明 */}
        <section className="about-android">
          <h2>{t('about.secAndroid')}</h2>
          <p>{t('about.androidDesc')}</p>
          <ul>
            <li>{t('about.androidPt1')}</li>
            <li>{t('about.androidPt2')}</li>
            <li>{t('about.androidPt3')}</li>
          </ul>
        </section>

        <footer className="about-footer">
          <span>MIT License · <a href="https://github.com/smanx/adbreeze" target="_blank" rel="noopener noreferrer">GitHub</a></span>
        </footer>
      </main>
    </div>
  )
}

/**
 * 单个功能大项：截图（cover 裁切，点击灯箱放大）与功能点清单左右排布，
 * flip = 图在右侧。每项一个对勾 + 名称 + 一句话说明。
 */
function FeatureSection({ icon, shot, title, flip, children }: {
  icon: string; shot: string; title: string; flip: boolean; children: React.ReactNode
}) {
  const [zoomed, setZoomed] = useState(false)
  return (
    <section className={'about-section' + (flip ? ' flip' : '')}>
      <h2 className="about-sec-title"><Icon name={icon} /> {title}</h2>
      <div className="about-section-body">
        <Shot shot={shot} title={title} icon={icon} onZoom={() => setZoomed(true)} />
        <ul className="about-fitems">{children}</ul>
      </div>
      {zoomed && <ZoomOverlay shot={shot} title={title} onClose={() => setZoomed(false)} />}
    </section>
  )
}

/** 大截图位：真图（悬停提示、点击放大）或 CSS 占位 */
function Shot({ shot, title, icon, onZoom }: {
  shot: string; title: string; icon: string; onZoom: () => void
}) {
  const t = useT()
  const [imgOk, setImgOk] = useState(true)
  const file = `./screenshots/${shot}.png`
  return (
    <button
      type="button"
      className={'about-shot' + (imgOk ? '' : ' is-placeholder')}
      onClick={imgOk ? onZoom : undefined}
      aria-label={imgOk ? t('about.shotZoomHint') : title}
      title={imgOk ? t('about.shotZoomHint') : undefined}
    >
      {imgOk && <span className="about-shot-hint">{t('about.shotZoomHint')}</span>}
      {imgOk
        ? <img src={file} alt={title} loading="lazy" onError={() => setImgOk(false)} />
        : (
          <div className="about-shot-ph">
            <span className="about-shot-ph-icon"><Icon name={icon} size={36} /></span>
            <span className="about-shot-ph-name">{title}</span>
            <span className="about-shot-ph-hint">screenshot · {shot}.png</span>
          </div>
        )}
    </button>
  )
}

/** 灯箱：fixed 全屏遮罩 + 原图 contain 完整显示，点击任意处或 Esc 关闭 */
function ZoomOverlay({ shot, title, onClose }: { shot: string; title: string; onClose: () => void }) {
  const t = useT()
  return (
    <div className="about-zoom" role="dialog" aria-modal="true" aria-label={title}
      onClick={onClose} onKeyDown={e => { if (e.key === 'Escape') onClose() }}
      tabIndex={-1} ref={el => el?.focus()}>
      <button type="button" className="about-zoom-close">{t('about.shotZoomClose')} ✕</button>
      <img src={`./screenshots/${shot}.png`} alt={title} />
    </div>
  )
}

/** 功能点：i18n 键约定 `about.<x>` = 名称，`about.<x>.desc` = 说明 */
function FeatureItem({ k }: { k: string }) {
  const t = useT()
  return (
    <li className="about-fitem">
      <span className="about-fitem-check">✓</span>
      <div>
        <b>{t(k as never)}</b>
        <p>{t(`${k}.desc` as never)}</p>
      </div>
    </li>
  )
}

/** 控制流程节点：大图标 + 短标签 + 角色名，accent = 服务端高亮 */
function FlowNode({ icon, label, sub, accent }: {
  icon: string; label: string; sub: string; accent?: boolean
}) {
  return (
    <div className={'about-flow-node' + (accent ? ' accent' : '')}>
      <span className="about-flow-node-icon"><Icon name={icon} size={26} /></span>
      <span className="about-flow-node-label">{label}</span>
      <span className="about-flow-node-sub">{sub}</span>
    </div>
  )
}

/** 控制端节点：浏览器大图标 + 各平台小图标行（表明任意设备经浏览器均可访问） */
function FlowController({ label }: { label: string }) {
  const t = useT()
  const PLATFORMS = ['windows', 'apple', 'linux', 'phone', 'tablet', 'harmony', 'globe']
  return (
    <div className="about-flow-node fx" style={{ '--i': 4 } as React.CSSProperties}>
      <span className="about-flow-node-icon"><Icon name="browser" size={26} /></span>
      <span className="about-flow-node-label">{label}</span>
      <span className="about-flow-node-sub">{t('about.flowClient')}</span>
      <span className="about-flow-platforms" title={t('about.flowPlatforms')} aria-hidden="true">
        {PLATFORMS.map((p, i) => (
          <span key={p} className="about-flow-platforms-item" style={{ '--pi': i } as React.CSSProperties}>
            <Icon name={p} size={15} />
          </span>
        ))}
      </span>
    </div>
  )
}

/**
 * 双向数据管道：一条带上下双车道的轨道，光点在两条车道上相向流动，
 * 两端是 ⇄ 箭头。vertical = 竖直（车道左右分布）。
 */
function FlowArrow({ vertical, double = false }: { vertical?: boolean; double?: boolean }) {
  void double
  return (
    <span className={'about-flow-pipe' + (vertical ? ' vert' : '')} aria-hidden="true">
      <svg className="about-flow-pipe-heads" width="30" height="20" viewBox="0 0 30 20" fill="none"
        stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <path d="M8 4l-4 6 4 6" /><path d="M22 4l4 6-4 6" />
      </svg>
      <span className="about-flow-pipe-track">
        <span className="about-flow-pipe-lane" />
        <span className="about-flow-packet fwd" /><span className="about-flow-packet fwd d2" />
        <span className="about-flow-packet back" /><span className="about-flow-packet back d2" />
      </span>
    </span>
  )
}

/** 箭头 + 协议标签 + 上下注释（上=局域网直连，下=CF 穿透） */
function FlowConnNote({ label, noteTop, noteBottom, index = 3 }: {
  label: string; noteTop: string; noteBottom: string; index?: number
}) {
  return (
    <div className="about-flow-connwrap fx" style={{ '--i': index } as React.CSSProperties}>
      <span className="about-flow-connnote top">{noteTop}</span>
      <FlowArrow double />
      <span className="about-flow-conn">{label}</span>
      <FlowArrow double />
      <span className="about-flow-connnote bottom">{noteBottom}</span>
    </div>
  )
}
