// 关于页（/about）：项目功能全景介绍，独立路由、无需登录。
// 双语复用全局 i18n 词典（跟随控制台右上角的语言切换 / localStorage 偏好）。
// 纯静态内容：不请求任何 API，登录前后均可访问。
import { toggleLang, useT } from '../i18n'
import { Icon } from './Icon'

export default function AboutPage() {
  const t = useT()
  // 回控制台：清掉 hash 即可（hashchange 触发 App 切回 Shell）；
  // href 保留当前路径（兼容 GitHub Pages 子路径部署），hash 指向根路由
  const toConsole = () => { location.hash = '#/' }

  return (
    <div className="about-page">
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
        {/* Hero */}
        <section className="about-hero">
          <h1>{t('about.heroTitle')}</h1>
          <p className="about-tagline">{t('about.heroTagline')}</p>
          <div className="about-badges">
            <span className="about-badge">Windows</span>
            <span className="about-badge">macOS</span>
            <span className="about-badge">Linux</span>
            <span className="about-badge">Docker</span>
            <span className="about-badge">Android</span>
            <span className="about-badge">{t('about.badgeSingleBinary')}</span>
            <span className="about-badge">{t('about.badgeWebOnly')}</span>
          </div>
        </section>

        {/* 核心功能：六大板块 */}
        <Section icon="monitor" title={t('about.secMirror')}>
          <Feature icon="mirror" name={t('about.fMirror.name')} desc={t('about.fMirror.desc')} />
          <Feature icon="mirror" name={t('about.fAudio.name')} desc={t('about.fAudio.desc')} />
          <Feature icon="mirror" name={t('about.fControl.name')} desc={t('about.fControl.desc')} />
          <Feature icon="mirror" name={t('about.fMultiView.name')} desc={t('about.fMultiView.desc')} />
        </Section>

        <Section icon="term" title={t('about.secControl')}>
          <Feature icon="term" name={t('about.fTerm.name')} desc={t('about.fTerm.desc')} />
          <Feature icon="term" name={t('about.fShell.name')} desc={t('about.fShell.desc')} />
          <Feature icon="term" name={t('about.fBatch.name')} desc={t('about.fBatch.desc')} />
          <Feature icon="term" name={t('about.fProcess.name')} desc={t('about.fProcess.desc')} />
        </Section>

        <Section icon="files" title={t('about.secFiles')}>
          <Feature icon="files" name={t('about.fFiles.name')} desc={t('about.fFiles.desc')} />
          <Feature icon="files" name={t('about.fApps.name')} desc={t('about.fApps.desc')} />
          <Feature icon="files" name={t('about.fApk.name')} desc={t('about.fApk.desc')} />
          <Feature icon="files" name={t('about.fClip.name')} desc={t('about.fClip.desc')} />
        </Section>

        <Section icon="overview" title={t('about.secMonitor')}>
          <Feature icon="overview" name={t('about.fOverview.name')} desc={t('about.fOverview.desc')} />
          <Feature icon="overview" name={t('about.fPerf.name')} desc={t('about.fPerf.desc')} />
          <Feature icon="overview" name={t('about.fLogcat.name')} desc={t('about.fLogcat.desc')} />
          <Feature icon="overview" name={t('about.fLayout.name')} desc={t('about.fLayout.desc')} />
        </Section>

        <Section icon="globe" title={t('about.secNet')}>
          <Feature icon="globe" name={t('about.fTunnel.name')} desc={t('about.fTunnel.desc')} />
          <Feature icon="globe" name={t('about.fTunnelNamed.name')} desc={t('about.fTunnelNamed.desc')} />
          <Feature icon="globe" name={t('about.fRemote.name')} desc={t('about.fRemote.desc')} />
          <Feature icon="globe" name={t('about.fDevices.name')} desc={t('about.fDevices.desc')} />
        </Section>

        <Section icon="settings" title={t('about.secInfra')}>
          <Feature icon="settings" name={t('about.fSingle.name')} desc={t('about.fSingle.desc')} />
          <Feature icon="settings" name={t('about.fHttps.name')} desc={t('about.fHttps.desc')} />
          <Feature icon="settings" name={t('about.fAuth.name')} desc={t('about.fAuth.desc')} />
          <Feature icon="settings" name={t('about.fPwa.name')} desc={t('about.fPwa.desc')} />
          <Feature icon="settings" name={t('about.fSelfHeal.name')} desc={t('about.fSelfHeal.desc')} />
          <Feature icon="settings" name={t('about.fI18n.name')} desc={t('about.fI18n.desc')} />
        </Section>

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

/** 分组标题条：图标 + 标题 */
function Section({ icon, title, children }: { icon: string; title: string; children: React.ReactNode }) {
  return (
    <section className="about-section">
      <h2 className="about-sec-title"><Icon name={icon} /> {title}</h2>
      <div className="about-grid">{children}</div>
    </section>
  )
}

/** 单个功能卡片 */
function Feature({ icon, name, desc }: { icon: string; name: string; desc: string }) {
  return (
    <div className="about-card">
      <div className="about-card-head"><Icon name={icon} /><b>{name}</b></div>
      <p>{desc}</p>
    </div>
  )
}
