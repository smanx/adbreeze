// 关于页（/#/about）：项目功能全景介绍，独立路由、无需登录。
// 双语复用全局 i18n 词典（跟随控制台右上角的语言切换 / localStorage 偏好）。
// 纯静态内容：不请求任何 API，登录前后均可访问。
//
// 版式：每个功能大项一个「截图 + 功能点」的横向板块——
//   截图（16:10，cover 裁切）居一侧，该大项下的所有功能点排在另一侧，
//   左右交替（zigzag）。鼠标悬停板块时截图平滑放大（scale），浏览细节。
//
// 截图替换：把图片放进 web/public/screenshots/ 并按 Section.shot 命名
// （如 mirror.png），构建后自动显示；文件缺失时显示 CSS 占位（img onError 回退）。
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
  // 回控制台：清掉 hash 即可（hashchange 触发 App 切回 Shell）；
  // href 保留当前路径（兼容 GitHub Pages 子路径部署），hash 指向根路由
  const toConsole = () => { location.hash = '#/' }
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
        <button className="btn tiny" onClick={toConsole}>{t('about.openConsole')}</button>
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
            <button className="about-cta about-cta-plain" onClick={toConsole}>
              {t('about.openConsole')} <span className="about-cta-arrow">→</span>
            </button>
          </div>
          {/* Docker 复制成功的命令回显 */}
          <div className={'about-docker-cmd' + (copied ? ' show' : '')}>
            <code>$ {DOCKER_CMD}</code>
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
 * 单个功能大项：截图（cover 裁切，悬停放大）与功能点清单左右排布，
 * flip = 图在右侧。每项一个对勾 + 名称 + 一句话说明。
 */
function FeatureSection({ icon, shot, title, flip, children }: {
  icon: string; shot: string; title: string; flip: boolean; children: React.ReactNode
}) {
  return (
    <section className={'about-section' + (flip ? ' flip' : '')}>
      <h2 className="about-sec-title"><Icon name={icon} /> {title}</h2>
      <div className="about-section-body">
        <Shot shot={shot} title={title} icon={icon} />
        <ul className="about-fitems">{children}</ul>
      </div>
    </section>
  )
}

/** 大截图位：真图（悬停浮出全尺寸预览）或 CSS 占位 */
function Shot({ shot, title, icon }: { shot: string; title: string; icon: string }) {
  const [imgOk, setImgOk] = useState(true)
  const [hover, setHover] = useState<{ x: number; y: number } | null>(null)
  const file = `./screenshots/${shot}.png`
  return (
    <div
      className={'about-shot' + (imgOk ? '' : ' is-placeholder')}
      onMouseMove={imgOk ? e => setHover({ x: e.clientX, y: e.clientY }) : undefined}
      onMouseLeave={imgOk ? () => setHover(null) : undefined}
    >
      {imgOk
        ? <img src={file} alt={title} loading="lazy" onError={() => setImgOk(false)} />
        : (
          <div className="about-shot-ph">
            <span className="about-shot-ph-icon"><Icon name={icon} size={36} /></span>
            <span className="about-shot-ph-name">{title}</span>
            <span className="about-shot-ph-hint">screenshot · {shot}.png</span>
          </div>
        )}
      {/* 悬浮预览：挂在 fixed 层，原图 contain 完整显示，跟鼠标位置避让 */}
      {hover && imgOk && (
        <div
          className="about-hover-preview"
          style={hoverPos(hover.x, hover.y)}
          aria-hidden="true"
        >
          <img src={file} alt="" />
        </div>
      )}
    </div>
  )
}

/**
 * 浮层定位：优先出现在鼠标同侧、垂直居中；贴近屏幕边缘时翻到另一侧。
 * 尺寸由 CSS 限定（max 82vw × 78vh，contain 完整显示），此处只算左上角坐标。
 */
function hoverPos(mx: number, my: number): React.CSSProperties {
  const w = Math.min(window.innerWidth * 0.82, 1200)
  const h = window.innerHeight * 0.78
  const gap = 20
  // 鼠标在屏幕左半 → 浮层放右边；反之放左边
  const left = mx < window.innerWidth / 2
    ? Math.min(mx + gap, window.innerWidth - w - 10)
    : Math.max(mx - gap - w, 10)
  const top = Math.max(10, Math.min(my - h / 2, window.innerHeight - h - 10))
  return { left, top }
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
