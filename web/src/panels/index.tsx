// 功能面板注册表：id ↔ 标题 ↔ 实现阶段
export const TABS = [
  { id: 'overview', label: '概览', phase: '' },
  { id: 'mirror', label: '投屏', phase: '' },
  { id: 'files', label: '文件', phase: '' },
  { id: 'apps', label: '应用', phase: '' },
  { id: 'process', label: '进程', phase: '' },
  { id: 'perf', label: '性能', phase: '' },
  { id: 'layout', label: '布局', phase: '' },
  { id: 'logcat', label: '日志', phase: '' },
  { id: 'term', label: '终端', phase: '' },
  { id: 'tools', label: '工具', phase: '' },
  { id: 'settings', label: '设置', phase: '' },
] as const

// 占位面板：尚未实现的功能页
export function Placeholder({ title, phase, selected }: { title: string; phase: string; selected: string | null }) {
  return (
    <div className="panel-placeholder">
      <h2>{title}</h2>
      <p className="hint">该面板将在 {phase} 实现，当前为布局占位。</p>
      <p className="hint">{selected ? <>当前设备：<b>{selected}</b></> : '请先在设备列表中选择设备'}</p>
    </div>
  )
}
