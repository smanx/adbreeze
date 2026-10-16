import { useT } from '../i18n'

// 功能面板注册表：id ↔ 标题 ↔ 实现阶段
// labelKey 指向 i18n 词典键（渲染时按当前语言取标题）
export const TABS = [
  { id: 'overview', labelKey: 'tab.overview', phase: '' },
  { id: 'mirror', labelKey: 'tab.mirror', phase: '' },
  { id: 'files', labelKey: 'tab.files', phase: '' },
  { id: 'apps', labelKey: 'tab.apps', phase: '' },
  { id: 'process', labelKey: 'tab.process', phase: '' },
  { id: 'perf', labelKey: 'tab.perf', phase: '' },
  { id: 'layout', labelKey: 'tab.layout', phase: '' },
  { id: 'logcat', labelKey: 'tab.logcat', phase: '' },
  { id: 'term', labelKey: 'tab.term', phase: '' },
  { id: 'tools', labelKey: 'tab.tools', phase: '' },
  { id: 'settings', labelKey: 'tab.settings', phase: '' },
] as const

// 占位面板：尚未实现的功能页
export function Placeholder({ title, phase, selected }: { title: string; phase: string; selected: string | null }) {
  const t = useT()
  return (
    <div className="panel-placeholder">
      <h2>{title}</h2>
      <p className="hint">{t('placeholder.phase', { phase })}</p>
      <p className="hint">{selected ? <>{t('placeholder.currentDevice')}<b>{selected}</b></> : t('panel.selectDevice')}</p>
    </div>
  )
}
