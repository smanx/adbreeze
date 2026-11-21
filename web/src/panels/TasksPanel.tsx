import { useCallback, useEffect, useRef, useState } from 'react'
import {
  listTasks, createTask, updateTask, deleteTask, runTask,
  listTaskRuns, deleteTaskRun, clearTaskRuns,
  type ScheduledTask, type RunRecord,
} from '../api'
import { useT } from '../i18n'
import { useStore } from '../store'
import { analyzeCron } from '../cron'

const emptyTask = (): ScheduledTask => ({
  id: '', name: '', cron: '*/5 * * * *', prompt: '', device: '', model: '', enabled: true,
})

function statusKey(status: string) {
  switch (status) {
    case 'running': return 'tasks.statusRunning' as const
    case 'success': return 'tasks.statusSuccess' as const
    case 'failed': return 'tasks.statusFailed' as const
    case 'timeout': return 'tasks.statusTimeout' as const
    default: return 'common.unknown' as const
  }
}

export default function TasksPanel() {
  const t = useT()
  // 目标设备必须从当前设备列表里选，不能手输。
  const devices = useStore(s => s.devices)
  const selectedDevice = useStore(s => s.selected)
  const [tasks, setTasks] = useState<ScheduledTask[]>([])
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')       // 列表级错误（加载 / 删除 / 执行）
  const [formErr, setFormErr] = useState('') // 编辑弹框内的校验与保存错误
  const [hint, setHint] = useState('')
  const [editing, setEditing] = useState<ScheduledTask | null>(null)
  const [form, setForm] = useState<ScheduledTask>(emptyTask())
  const [runningId, setRunningId] = useState('')
  const [recordsFor, setRecordsFor] = useState<string | null>(null)
  const [records, setRecords] = useState<RunRecord[]>([])
  const [detail, setDetail] = useState<RunRecord | null>(null)
  const recordTimer = useRef<ReturnType<typeof setInterval> | null>(null)

  const load = useCallback(async () => {
    setLoading(true); setErr('')
    try { setTasks(await listTasks()) }
    catch (e) { setErr((e as Error).message) }
    finally { setLoading(false) }
  }, [])

  useEffect(() => { void load() }, [load])

  const loadRecords = useCallback(async (id: string) => {
    try { setRecords(await listTaskRuns(id, 200)) }
    catch (e) { setErr((e as Error).message) }
  }, [])

  // 执行记录抽屉打开时 3s 轮询（running 状态需要自动刷新）
  useEffect(() => {
    if (!recordsFor) return
    void loadRecords(recordsFor)
    recordTimer.current = setInterval(() => void loadRecords(recordsFor), 3000)
    return () => { if (recordTimer.current) clearInterval(recordTimer.current) }
  }, [recordsFor, loadRecords])

  const openNew = () => {
    // 默认带上当前选中的设备（仍需确认保存），没有选中则强制用户从列表里挑。
    const seed = { ...emptyTask(), device: devices.some(d => d.serial === selectedDevice) ? selectedDevice || '' : '' }
    setFormErr('')
    setForm(seed); setEditing({ ...seed, id: '__new__' })
  }
  const openEdit = (tk: ScheduledTask) => { setFormErr(''); setForm({ ...tk }); setEditing({ ...tk }) }

  const save = async () => {
    const cron = analyzeCron(form.cron)
    if (!cron.valid) { setFormErr(cron.error); return }
    if (!form.name.trim()) { setFormErr(t('tasks.fieldName')); return }
    if (!form.prompt.trim()) { setFormErr(t('tasks.fieldPrompt')); return }
    if (!form.device.trim()) { setFormErr(t('tasks.deviceRequired')); return }
    if (!devices.some(d => d.serial === form.device)) { setFormErr(t('tasks.deviceNotInList')); return }
    setFormErr(''); setHint('')
    try {
      const payload: ScheduledTask = {
        ...form,
        id: form.id && form.id !== '__new__' ? form.id : '',
        name: form.name.trim(), cron: form.cron.trim(), prompt: form.prompt.trim(),
      }
      if (payload.id) await updateTask(payload.id, payload)
      else await createTask(payload)
      setHint(t('tasks.saved'))
      setEditing(null)
      await load()
    } catch (e) { setFormErr((e as Error).message) }
  }

  const remove = async (tk: ScheduledTask) => {
    if (!confirm(t('tasks.deleteConfirm', { name: tk.name }))) return
    try {
      await deleteTask(tk.id)
      if (recordsFor === tk.id) setRecordsFor(null)
      await load()
    } catch (e) { setErr((e as Error).message) }
  }

  const toggle = async (tk: ScheduledTask) => {
    try { await updateTask(tk.id, { ...tk, enabled: !tk.enabled }); await load() }
    catch (e) { setErr((e as Error).message) }
  }

  const run = async (tk: ScheduledTask) => {
    setRunningId(tk.id); setHint(''); setErr('')
    try {
      await runTask(tk.id)
      setHint(t('tasks.runStarted'))
      setRecordsFor(tk.id)
    } catch (e) { setErr((e as Error).message) }
    finally { setRunningId('') }
  }

  const removeRecord = async (rid: string, id: string) => {
    if (!confirm(t('tasks.deleteRecordConfirm'))) return
    try { await deleteTaskRun(id, rid); await loadRecords(id); if (detail?.id === rid) setDetail(null) }
    catch (e) { setErr((e as Error).message) }
  }

  const clear = async (id: string) => {
    if (!confirm(t('tasks.clearConfirm'))) return
    try { await clearTaskRuns(id); await loadRecords(id) }
    catch (e) { setErr((e as Error).message) }
  }

  const field = (key: keyof ScheduledTask, value: string | boolean) => {
    setFormErr('')
    setForm(f => ({ ...f, [key]: value }))
  }
  const cronInfo = analyzeCron(form.cron)

  return (
    <div className="panel tasks-panel">
      <div className="tasks-bar">
        <div>
          <h2 style={{ margin: 0 }}>{t('tasks.title')}</h2>
          <p className="hint" style={{ margin: '4px 0 0' }}>{t('tasks.subtitle')}</p>
        </div>
        <span className="spacer" />
        {hint && <span className="hint">{hint}</span>}
        <button className="btn ghost" onClick={() => void load()}>{t('tasks.refresh')}</button>
        <button className="btn" onClick={openNew}>{t('tasks.new')}</button>
      </div>
      {err && <p className="error-text">{err}</p>}

      {!tasks.length && !loading && <p className="hint tasks-empty">{t('tasks.empty')}</p>}

      <div className="tasks-list">
        {tasks.map(tk => (
          <div className="task-card" key={tk.id}>
            <div className="task-card-head">
              <div className="task-card-title">{tk.name}</div>
              <label className="task-switch">
                <input type="checkbox" checked={tk.enabled} onChange={() => void toggle(tk)} />
                {tk.enabled ? t('tasks.enabled') : t('tasks.disabled')}
              </label>
            </div>
            <div className="task-card-meta">
              <span className="task-cron mono">{tk.cron}</span>
              {tk.device && <span className="hint">· {tk.device}</span>}
            </div>
            <div className="task-card-prompt">{tk.prompt}</div>
            <div className="task-card-actions">
              <button className="btn ghost" disabled={runningId === tk.id} onClick={() => void run(tk)}>
                {runningId === tk.id ? t('common.loading') : t('tasks.run')}
              </button>
              <button className="btn ghost" onClick={() => { setRecordsFor(tk.id); setDetail(null) }}>{t('tasks.records')}</button>
              <button className="btn ghost" onClick={() => openEdit(tk)}>{t('tasks.edit')}</button>
              <button className="btn ghost danger" onClick={() => void remove(tk)}>{t('tasks.delete')}</button>
            </div>
          </div>
        ))}
      </div>

      {/* 任务编辑弹窗 */}
      {editing && (
        <div className="apps-modal-mask">
          <div className="apps-modal task-modal" onClick={e => e.stopPropagation()}>
            <h3>{editing.id === '__new__' ? t('tasks.new') : t('tasks.edit')}</h3>
            {/* 校验/保存错误只出现在弹框内，不再跑到后面的任务列表里 */}
            {formErr && <p className="error-text modal-error">{formErr}</p>}
            <div className="task-form">
              <label>{t('tasks.fieldName')}
                <input type="text" value={form.name} onChange={e => field('name', e.target.value)} />
              </label>
              <label>{t('tasks.fieldCron')}
                <input type="text" className="mono" value={form.cron} onChange={e => field('cron', e.target.value)} />
              </label>
              <p className="hint">{t('tasks.fieldCronHint')}</p>
              {cronInfo.valid
                ? <p className="cron-desc">{t('tasks.cronRuns', { desc: cronInfo.description })}</p>
                : <p className="error-text modal-error">{cronInfo.error}</p>}
              <label>{t('tasks.fieldPrompt')}
                <textarea rows={5} value={form.prompt} onChange={e => field('prompt', e.target.value)} />
              </label>
              <p className="hint">{t('tasks.fieldPromptHint')}</p>
              <label>{t('tasks.fieldDevice')}
                <select value={form.device} onChange={e => field('device', e.target.value)}>
                  <option value="">{t('tasks.devicePlaceholder')}</option>
                  {devices.map(d => (
                    <option key={d.serial} value={d.serial}>
                      {d.serial}{d.model ? ` · ${d.model}` : ''}{d.state === 'offline' ? ` (${t('tasks.deviceOffline')})` : ''}
                    </option>
                  ))}
                </select>
              </label>
              <p className="hint">{t('tasks.fieldDeviceHint')}</p>
              <label className="task-form-chk">
                <input type="checkbox" checked={form.enabled} onChange={e => field('enabled', e.target.checked)} />
                {t('tasks.enabled')}
              </label>
            </div>
            <div className="apps-modal-foot">
              <button className="btn" onClick={() => void save()}>{t('tasks.save')}</button>
              <button className="btn ghost" onClick={() => setEditing(null)}>{t('common.close')}</button>
            </div>
          </div>
        </div>
      )}

      {/* 执行记录抽屉 */}
      {recordsFor && (
        <div className="apps-modal-mask">
          <div className="apps-modal runs-modal" onClick={e => e.stopPropagation()}>
            <div className="runs-head">
              <h3>{t('tasks.records')}</h3>
              <span className="spacer" />
              <button className="btn ghost danger" onClick={() => void clear(recordsFor)}>{t('tasks.clearRecords')}</button>
              <button className="btn ghost" onClick={() => { setRecordsFor(null); setDetail(null) }}>{t('common.close')}</button>
            </div>
            {!records.length && <p className="hint">{t('tasks.recordsEmpty')}</p>}
            <div className="runs-list">
              {records.map(r => (
                <div className={`run-row status-${r.status}`} key={r.id} onClick={() => setDetail(r)}>
                  <span className={`run-status run-${r.status}`}>{t(statusKey(r.status))}</span>
                  <span className="mono hint">{r.finishedAt || r.startedAt}</span>
                  <span className="hint">{r.trigger === 'cron' ? t('tasks.triggerCron') : t('tasks.triggerManual')}</span>
                  <span className="hint">{r.durationMs} ms</span>
                  <button className="btn ghost danger run-del" onClick={e => { e.stopPropagation(); void removeRecord(r.id, recordsFor) }}>{t('common.delete')}</button>
                </div>
              ))}
            </div>
            {detail && (
              <div className="run-detail">
                <h4>{t('tasks.runOutput')} · {detail.id}</h4>
                {detail.error && <p className="error-text">{t('tasks.runError')}: {detail.error}</p>}
                <pre className="run-output">{detail.output || t('common.noOutput')}</pre>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
