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
import { useState } from 'react'
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
      {/* 背景装饰层：极光渐变 + 网格，纯 CSS 不耗资源 */}
      <div className="about-bg" aria-hidden="true">
        <div className="about-bg-aurora" />
        <div className="about-bg-grid" />
      </div>

      <header className="about-topbar">
        <span className="brand">ADBreeze</span>
        <span className="about-features-badge">Features</span>
        <span className="spacer" />
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
        <section className="about-flow">
          <h2><Icon name="globe" size={20} /> {t('about.secFlow')}</h2>

          {/* 场景一：被控端与服务端同在一台安卓手机（APK 自身即服务端） */}
          <div className="about-flow-scene">
            <div className="about-flow-scene-title">
              <Icon name="overview" size={16} /> {t('about.flowSame')}
            </div>
            <div className="about-flow-chain">
              {/* 被控端与服务端同机：两个节点包在同一个「手机」框里 */}
              <div className="about-flow-group">
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
            <p className="about-flow-note">{t('about.flowSameNote')}</p>
          </div>

          {/* 场景二：被控端与服务端分离（服务端可跑在电脑 / Docker / 另一台安卓上） */}
          <div className="about-flow-scene">
            <div className="about-flow-scene-title">
              <Icon name="laptop" size={16} /> {t('about.flowSplit')}
            </div>
            <div className="about-flow-split">
              <div className="about-flow-devices">
                <FlowNode icon="overview" label={t('about.flowDevPhone')} sub={t('about.flowDevice')} />
                <FlowNode icon="overview" label={t('about.flowDevTv')} sub={t('about.flowDevice')} />
                <FlowNode icon="overview" label={t('about.flowDevMore')} sub={t('about.flowDevice')} />
              </div>
              <div className="about-flow-split-mid">
                <FlowArrow double />
                <span className="about-flow-conn">{t('about.flowUsbWifi')}</span>
                <FlowArrow double />
              </div>
              <div className="about-flow-servers">
                <FlowNode icon="laptop" label="Windows" sub={t('about.flowServer')} accent />
                <FlowNode icon="laptop" label="macOS / Linux" sub={t('about.flowServer')} accent />
                <FlowNode icon="docker" label="Docker" sub={t('about.flowServer')} accent />
                <FlowNode icon="overview" label={t('about.flowServerAndroid')} sub={t('about.flowServer')} accent />
              </div>
              <FlowConnNote label={t('about.flowHttp')} noteTop={t('about.flowLan')} noteBottom={t('about.flowCf')} />
              <FlowController label={t('about.flowBrowserAny')} />
            </div>
            <p className="about-flow-note">{t('about.flowSplitNote')}</p>
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
  return (
    <div className="about-flow-node">
      <span className="about-flow-node-icon"><Icon name="browser" size={26} /></span>
      <span className="about-flow-node-label">{label}</span>
      <span className="about-flow-node-sub">{t('about.flowClient')}</span>
      <span className="about-flow-platforms" title={t('about.flowPlatforms')} aria-hidden="true">
        <Icon name="windows" size={15} />
        <Icon name="apple" size={15} />
        <Icon name="linux" size={15} />
        <Icon name="phone" size={15} />
        <Icon name="tablet" size={15} />
        <Icon name="harmony" size={15} />
        <Icon name="globe" size={15} />
      </span>
    </div>
  )
}

/** 双向箭头：默认水平（⇄），vertical = 竖直（⇅） */
function FlowArrow({ vertical, double = false }: { vertical?: boolean; double?: boolean }) {
  void double
  return (
    <span className={'about-flow-arrow' + (vertical ? ' vert' : '')} aria-hidden="true">
      <svg width="26" height="14" viewBox="0 0 26 14" fill="none" stroke="currentColor"
        strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <path d="M5 4l-3 3 3 3" /><path d="M21 4l3 3-3 3" /><path d="M2.6 7h20.8" />
      </svg>
    </span>
  )
}

/** 箭头 + 协议标签 + 上下注释（上=局域网直连，下=CF 穿透） */
function FlowConnNote({ label, noteTop, noteBottom }: { label: string; noteTop: string; noteBottom: string }) {
  return (
    <div className="about-flow-connwrap">
      <span className="about-flow-connnote top">{noteTop}</span>
      <FlowArrow double />
      <span className="about-flow-conn">{label}</span>
      <FlowArrow double />
      <span className="about-flow-connnote bottom">{noteBottom}</span>
    </div>
  )
}
