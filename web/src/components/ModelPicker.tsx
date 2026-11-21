import { useEffect, useMemo, useRef, useState } from 'react'
import { useT } from '../i18n'

// 模型选择器：可输入过滤 + 按 provider 分组的下拉列表。
//
// 为什么不用 <datalist>：datalist 只在输入时给出零散建议，无法一眼浏览
// 上百个模型，也不显示 provider 分组；而 `opencode models` 往往返回 100+ 项。
// 这里用一个受控输入 + 自绘列表，既支持直接手输（自定义模型 ID），
// 也支持从列表中点选。
export default function ModelPicker({
  value, models, onChange, placeholder, disabled,
}: {
  value: string
  models: string[]
  onChange: (v: string) => void
  placeholder?: string
  disabled?: boolean
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const boxRef = useRef<HTMLDivElement>(null)

  // 点击外部关闭
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  // 输入框里的文字既用于过滤，也是最终值；打开列表时用当前值作为初始过滤。
  const filter = (open ? query : value).trim().toLowerCase()
  const filtered = useMemo(() => {
    if (!filter) return models
    return models.filter(m => m.toLowerCase().includes(filter))
  }, [models, filter])

  // 按 provider 分组（模型 ID 形如 providerID/modelID，取第一段）
  const groups = useMemo(() => {
    const map = new Map<string, string[]>()
    for (const m of filtered) {
      const i = m.indexOf('/')
      const key = i > 0 ? m.slice(0, i) : m
      const arr = map.get(key)
      if (arr) arr.push(m)
      else map.set(key, [m])
    }
    return Array.from(map.entries()).sort((a, b) => a[0].localeCompare(b[0]))
  }, [filtered])

  const pick = (m: string) => {
    onChange(m)
    setQuery('')
    setOpen(false)
  }

  return (
    <div className="model-picker" ref={boxRef}>
      <input
        type="text"
        className="mono"
        value={open ? query : value}
        placeholder={placeholder}
        disabled={disabled}
        autoComplete="off"
        spellCheck={false}
        onFocus={() => { setQuery(value); setOpen(true) }}
        onChange={e => { setQuery(e.target.value); onChange(e.target.value); setOpen(true) }}
        onKeyDown={e => {
          if (e.key === 'Escape') setOpen(false)
          if (e.key === 'Enter') setOpen(false)
        }}
      />
      {open && (
        <div className="model-list" role="listbox">
          {!models.length && <div className="model-empty">{t('settings.agent.modelEmpty')}</div>}
          {models.length > 0 && !filtered.length && <div className="model-empty">{t('settings.agent.modelNoMatch')}</div>}
          {groups.map(([provider, items]) => (
            <div className="model-group" key={provider}>
              <div className="model-group-head">{provider}</div>
              {items.map(m => (
                <button
                  type="button"
                  role="option"
                  aria-selected={m === value}
                  key={m}
                  className={'model-item mono' + (m === value ? ' active' : '')}
                  // mousedown 早于 blur，避免点选时列表先被关闭
                  onMouseDown={e => { e.preventDefault(); pick(m) }}
                >
                  {m}
                </button>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
