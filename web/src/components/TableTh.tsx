import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useT } from '../i18n'

// 通用表格增强：
// 1. 可排序表头（点击切换升降序，▲/▼ 指示，与应用面板样式一致）；
// 2. 列宽拖拽（表头右缘 6px 拖动条，拖动时全局捕获，宽度存 localStorage）。
// 用法：把原 <th> 替换为 <Th>，在拖动把手 mousedown 时 stopPropagation 避免触发排序。
// columns: 每列 key/text；widths/setWidths 由调用方持有（px，0 表示未设置）。

export interface SortSpec<K extends string> {
  key: K
  asc: boolean
}

// 可排序 + 可拖宽表头单元格
export function Th<K extends string>(props: {
  colKey: K
  text: string
  sort?: SortSpec<K> | null
  onSort?: (k: K) => void
  width?: number
  onResize?: (w: number) => void
  align?: 'left' | 'right'
}) {
  const { colKey, text, sort, onSort, width, onResize, align = 'left' } = props
  const resizing = useRef(false)
  const startX = useRef(0)
  const startW = useRef(0)

  const onResizeStart = (e: React.MouseEvent) => {
    if (!onResize) return
    e.preventDefault()
    e.stopPropagation()
    resizing.current = true
    startX.current = e.clientX
    startW.current = width || (e.currentTarget.parentElement as HTMLElement).offsetWidth
    const move = (ev: MouseEvent) => {
      if (!resizing.current) return
      const w = Math.max(36, startW.current + ev.clientX - startX.current)
      onResize(w)
    }
    const up = () => {
      resizing.current = false
      document.removeEventListener('mousemove', move)
      document.removeEventListener('mouseup', up)
      document.body.style.cursor = ''
    }
    document.addEventListener('mousemove', move)
    document.addEventListener('mouseup', up)
    document.body.style.cursor = 'col-resize'
  }

  const sortable = !!onSort
  const active = sort?.key === colKey
  return (
    <th
      className={(sortable ? 'sortable' : '') + (onResize ? ' col-resizable' : '') + (align === 'right' ? ' num' : '')}
      style={width ? { width, minWidth: width, maxWidth: width } : undefined}
      onClick={sortable ? () => onSort!(colKey) : undefined}
    >
      {text}
      {active ? (sort!.asc ? ' ▲' : ' ▼') : ''}
      {onResize && (
        <span className="col-resizer" onMouseDown={onResizeStart} onClick={e => e.stopPropagation()} />
      )}
    </th>
  )
}

// 列宽持久化（localStorage，按表名隔离）
export function useColWidths(storageKey: string, count: number): [number[], (i: number, w: number) => void] {
  const [widths, setWidths] = useState<number[]>(() => {
    try {
      const raw = localStorage.getItem(storageKey)
      if (raw) {
        const arr = JSON.parse(raw)
        if (Array.isArray(arr) && arr.length === count) return arr as number[]
      }
    } catch { /* 忽略 */ }
    return new Array(count).fill(0)
  })
  const set = (i: number, w: number) =>
    setWidths(prev => {
      const n = [...prev]
      n[i] = w
      try { localStorage.setItem(storageKey, JSON.stringify(n)) } catch { /* 忽略 */ }
      return n
    })
  return [widths, set]
}

// 预览弹窗（截图预览/下载）
export function PreviewModal(props: {
  url: string
  title: string
  filename: string
  onClose: () => void
  footer?: ReactNode
}) {
  const { url, title, filename, onClose, footer } = props
  const t = useT()
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])
  const download = () => {
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
  }
  return (
    <div className="modal-mask" onClick={onClose}>
      <div className="preview-modal" onClick={e => e.stopPropagation()}>
        <div className="preview-head">
          <h3>{title}</h3>
          <button className="btn ghost tiny" onClick={onClose}>{t('common.close')}</button>
        </div>
        <div className="preview-body">
          <img src={url} alt={title} />
        </div>
        <div className="preview-foot">
          {footer}
          <button className="btn" onClick={download}>{t('preview.downloadPng')}</button>
        </div>
      </div>
    </div>
  )
}
