import { useEffect, useMemo, useRef, useState } from 'react'
import { api, apiUrl, authHeaders, isCrossOrigin } from '../api'
import { useStore } from '../store'
import { useT } from '../i18n'

interface FileEntry { name: string; size: number; mode: number; time: number; dir: boolean }

// 传输队列任务（上传进度展示）
interface Xfer {
  id: number
  name: string       // 显示名（含子目录相对路径）
  size: number
  loaded: number
  status: 'uploading' | 'done' | 'error'
  error?: string
}

function fmtSize(n: number) {
  if (n === 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0, v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`
}
function fmtTime(t: number) {
  if (!t) return '-'
  const d = new Date(t * 1000)
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function joinPath(dir: string, name: string) {
  return dir === '/' ? '/' + name : dir + '/' + name
}

let xferSeq = 1

// 文件管理器：目录导航 + 过滤 + 勾选批量操作（全选/反选/批量下载/批量删除）+
// 上传/下载（含文件夹 zip）/重命名/删除/新建目录 + 拖拽上传 + 进度
export default function FilesPanel() {
  const selected = useStore(s => s.selected)
  const [path, setPath] = useState('/sdcard')
  const [entries, setEntries] = useState<FileEntry[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [filter, setFilter] = useState('')
  const [dragOver, setDragOver] = useState(false)
  const [xfers, setXfers] = useState<Xfer[]>([])
  const [checked, setChecked] = useState<Set<string>>(new Set()) // 选中项名称集合（当前目录内）
  const [batchBusy, setBatchBusy] = useState(false)
  const uploadRef = useRef<HTMLInputElement>(null)
  const dirUploadRef = useRef<HTMLInputElement>(null)
  const t = useT()

  useEffect(() => { if (selected) list(path).catch(() => {}) /* 设备切换回到默认目录 */ }, [selected]) // eslint-disable-line react-hooks/exhaustive-deps

  const list = async (p: string) => {
    if (!selected) return
    setBusy(true); setError('')
    try {
      const data = await api<FileEntry[]>(`GET`, `/api/devices/${encodeURIComponent(selected)}/files?path=${encodeURIComponent(p)}`)
      setEntries([...data].sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1)))
      setPath(p)
      setChecked(new Set()) // 切目录后清空选择，避免对不可见项误操作
    } catch (e) {
      setError((e as Error).message)
    } finally { setBusy(false) }
  }

  const parentOf = (p: string) => {
    const i = p.lastIndexOf('/')
    return i <= 0 ? '/' : p.slice(0, i)
  }

  // 模糊过滤：不区分大小写，所有字符按顺序出现即命中（子序列匹配）
  const filtered = useMemo(() => {
    const kw = filter.trim().toLowerCase()
    if (!kw) return entries
    return entries.filter(e => {
      const n = e.name.toLowerCase()
      let i = 0
      for (const ch of kw) {
        i = n.indexOf(ch, i)
        if (i < 0) return false
        i++
      }
      return true
    })
  }, [entries, filter])

  // 过滤变化时剔除不在当前过滤结果中的选中项
  useEffect(() => {
    setChecked(prev => {
      const visible = new Set(filtered.map(e => e.name))
      const next = new Set([...prev].filter(n => visible.has(n)))
      return next.size === prev.size ? prev : next
    })
  }, [filtered])

  const toggleOne = (name: string) => {
    setChecked(prev => {
      const n = new Set(prev)
      if (n.has(name)) n.delete(name)
      else n.add(name)
      return n
    })
  }
  const selectAll = () => setChecked(new Set(filtered.map(e => e.name)))
  const invertSelection = () => {
    setChecked(prev => new Set(filtered.filter(e => !prev.has(e.name)).map(e => e.name)))
  }
  const clearSelection = () => setChecked(new Set())
  const checkedEntries = useMemo(() => filtered.filter(e => checked.has(e.name)), [filtered, checked])
  const allChecked = filtered.length > 0 && checkedEntries.length === filtered.length

  const download = async (e: FileEntry) => {
    if (!selected) return
    const full = joinPath(path, e.name)
    const url = `/api/devices/${encodeURIComponent(selected)}/files/content?path=${encodeURIComponent(full)}` +
      (e.dir ? '&zip=1' : '')
    if (isCrossOrigin()) {
      // 跨源：导航下载带不上 Authorization 头，改用 fetch 取 blob 再触发下载
      try {
        const res = await fetch(apiUrl(url), { headers: authHeaders() })
        if (!res.ok) throw new Error('HTTP ' + res.status)
        const blob = await res.blob()
        const a = document.createElement('a')
        a.href = URL.createObjectURL(blob)
        a.download = e.dir ? e.name + '.zip' : e.name
        a.click()
        setTimeout(() => URL.revokeObjectURL(a.href), 1000)
      } catch (err) { setError(t('files.downloadFailed') + (err as Error).message) }
      return
    }
    // 同源导航自动携带会话 cookie，无需再拼 token
    const a = document.createElement('a')
    a.href = url
    a.download = e.dir ? e.name + '.zip' : e.name
    a.click() // 浏览器原生下载，自带进度条
  }

  const remove = async (e: FileEntry) => {
    const type = e.dir ? t('files.dir') : t('files.file')
    const warn = e.dir ? t('files.confirmDeleteDirWarn') : ''
    if (!confirm(t('files.confirmDelete', { type, name: e.name, warn }))) return
    try {
      await api('DELETE', `/api/devices/${encodeURIComponent(selected!)}/files?path=${encodeURIComponent(joinPath(path, e.name))}`)
      await list(path)
    } catch (err) { setError((err as Error).message) }
  }

  // ---------- 批量操作 ----------
  const batchDelete = async () => {
    if (!checkedEntries.length) return
    const dirs = checkedEntries.filter(e => e.dir).length
    const files = checkedEntries.length - dirs
    const msg = t('files.confirmBatchDelete', { n: checkedEntries.length, d: dirs, f: files })
    if (!confirm(msg)) return
    setBatchBusy(true); setError('')
    let failed = 0
    for (const e of checkedEntries) {
      try {
        await api('DELETE', `/api/devices/${encodeURIComponent(selected!)}/files?path=${encodeURIComponent(joinPath(path, e.name))}`)
      } catch { failed++ }
    }
    setBatchBusy(false)
    if (failed) setError(t('files.batchFailed', { n: failed }))
    await list(path) // list 会清空选择
  }

  const batchDownload = () => {
    if (!checkedEntries.length) return
    // 逐个触发浏览器原生下载（文件夹走 zip）；间隔 300ms 避免浏览器拦截连续下载
    checkedEntries.forEach((e, i) => setTimeout(() => void download(e), i * 300))
  }

  const mkdir = async () => {
    const name = prompt(t('files.newDirPrompt'))
    if (!name) return
    try {
      await api('POST', `/api/devices/${encodeURIComponent(selected!)}/files/mkdir?path=${encodeURIComponent(joinPath(path, name))}`)
      await list(path)
    } catch (err) { setError((err as Error).message) }
  }

  const rename = async (e: FileEntry) => {
    const newName = prompt(t('files.renamePrompt', { type: e.dir ? t('files.folder') : t('files.file') }), e.name)
    if (!newName || newName === e.name) return
    if (/[\\/]/.test(newName)) { setError(t('files.badName')); return }
    try {
      await api('POST', `/api/devices/${encodeURIComponent(selected!)}/files/rename`, {
        from: joinPath(path, e.name),
        to: joinPath(path, newName),
      })
      await list(path)
    } catch (err) { setError((err as Error).message) }
  }

  // 上传单个文件（XHR 带进度），返回 Promise
  const uploadOne = (file: File, displayName: string, destDir: string) => new Promise<void>((resolve, reject) => {
    const id = xferSeq++
    const x: Xfer = { id, name: displayName, size: file.size, loaded: 0, status: 'uploading' }
    setXfers(q => [x, ...q].slice(0, 30)) // 队列最多显示 30 条
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', apiUrl(`/api/devices/${encodeURIComponent(selected!)}/files?path=${encodeURIComponent(joinPath(destDir, file.name))}`))
    for (const [k, v] of Object.entries(authHeaders())) xhr.setRequestHeader(k, v) // 跨源：XHR 不会自动带会话凭证
    xhr.upload.onprogress = ev => {
      if (ev.lengthComputable) {
        setXfers(q => q.map(tt => tt.id === id ? { ...tt, loaded: ev.loaded } : tt))
      }
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        setXfers(q => q.map(tt => tt.id === id ? { ...tt, status: 'done', loaded: file.size } : tt))
        resolve()
      } else {
        const msg = `HTTP ${xhr.status}`
        setXfers(q => q.map(tt => tt.id === id ? { ...tt, status: 'error', error: msg } : tt))
        reject(new Error(msg))
      }
    }
    xhr.onerror = () => {
      setXfers(q => q.map(tt => tt.id === id ? { ...tt, status: 'error', error: t('common.networkError') } : tt))
      reject(new Error(t('common.networkError')))
    }
    xhr.send(file)
  })

  // 上传一组文件：先补齐子目录，再逐个上传（串行避免 adb sync 通道争抢）
  const uploadMany = async (items: { file: File; relDir: string }[]) => {
    if (!selected || !items.length) return
    setError('')
    try {
      // 收集需要创建的子目录（相对当前 path）
      const dirs = new Set<string>()
      for (const it of items) {
        if (!it.relDir) continue
        const parts = it.relDir.split('/')
        let cur = path
        for (const part of parts) {
          cur = joinPath(cur, part)
          dirs.add(cur)
        }
      }
      for (const d of dirs) {
        await api('POST', `/api/devices/${encodeURIComponent(selected)}/files/mkdir?path=${encodeURIComponent(d)}`).catch(() => { /* 已存在 */ })
      }
      for (const it of items) {
        await uploadOne(it.file, it.relDir ? `${it.relDir}/${it.file.name}` : it.file.name, path)
      }
      await list(path)
    } catch (err) { setError((err as Error).message) }
  }

  const onPickFiles = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files || [])
    uploadMany(files.map(f => ({ file: f, relDir: '' })))
    e.target.value = ''
  }

  // 文件夹选择（webkitdirectory）：所有文件带 webkitRelativePath
  const onPickDir = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files || [])
    const items = files.map(f => {
      const rel = (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name
      const relDir = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')).replace(/^[^/]+\//, '') : ''
      return { file: f, relDir }
    })
    uploadMany(items)
    e.target.value = ''
  }

  // 拖拽上传：文件或整个文件夹（DataTransferItem.webkitGetAsEntry 递归遍历）
  const onDrop = async (e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    if (!selected) { setError(t('panel.selectDeviceShort')); return }
    const items = Array.from(e.dataTransfer.items || [])
    const results: { file: File; relDir: string }[] = []
    const walkEntry = async (entry: FileSystemEntry, relDir: string): Promise<void> => {
      if (entry.isFile) {
        const fe = entry as FileSystemFileEntry & {
          getFile: (ok: (f: File) => void, err?: (e: unknown) => void) => void
        }
        await new Promise<void>(res => fe.getFile(f => { results.push({ file: f, relDir }); res() }, () => res()))
      } else if (entry.isDirectory) {
        const de = entry as FileSystemDirectoryEntry
        const reader = de.createReader()
        // readEntries 每次最多返回 100 条，需循环读完
        for (;;) {
          const batch = await new Promise<FileSystemEntry[]>(res => reader.readEntries(
            es => res(es as unknown as FileSystemEntry[]), () => res([])))
          if (!batch.length) break
          for (const sub of batch) await walkEntry(sub, relDir ? `${relDir}/${de.name}` : de.name)
        }
      }
    }
    // 根级目录的子项 relDir 为目录名本身
    const roots = items.map(i => (i.webkitGetAsEntry?.() as FileSystemEntry | null)).filter(Boolean) as FileSystemEntry[]
    for (const root of roots) {
      if (root.isFile) {
        await walkEntry(root, '')
      } else {
        // 目录：先进入目录，子项 relDir = 目录名
        const de = root as FileSystemDirectoryEntry
        const reader = de.createReader()
        for (;;) {
          const batch = await new Promise<FileSystemEntry[]>(res => reader.readEntries(
            es => res(es as unknown as FileSystemEntry[]), () => res([])))
          if (!batch.length) break
          for (const sub of batch) await walkEntry(sub, de.name)
        }
      }
    }
    if (results.length) uploadMany(results)
  }

  if (!selected) return <div className="panel"><p className="hint">{t('panel.selectDevice.')}</p></div>

  const activeXfers = xfers.filter(x => x.status === 'uploading').length
  const totalLoaded = xfers.reduce((s, x) => s + x.loaded, 0)
  const totalSize = xfers.reduce((s, x) => s + x.size, 0)

  return (
    <div className={`panel files-panel${dragOver ? ' drag-over' : ''}`}
      onDragOver={e => { e.preventDefault(); setDragOver(true) }}
      onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragOver(false) }}
      onDrop={onDrop}>
      <div className="files-bar">
        <button className="btn tiny" onClick={() => list(parentOf(path))} disabled={path === '/'}>{t('files.up')}</button>
        <input className="files-path" value={path} onChange={e => setPath(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && list(path)} />
        <button className="btn tiny" onClick={() => list(path)} disabled={busy}>{t('common.refresh')}</button>
        <button className="btn tiny" onClick={mkdir}>{t('files.newDir')}</button>
        <button className="btn tiny" onClick={() => uploadRef.current?.click()}>{t('files.uploadFiles')}</button>
        <button className="btn tiny" onClick={() => dirUploadRef.current?.click()}>{t('files.uploadDir')}</button>
        <input ref={uploadRef} type="file" multiple style={{ display: 'none' }} onChange={onPickFiles} />
        <input ref={dirUploadRef} type="file" style={{ display: 'none' }}
          {...({ webkitdirectory: '', directory: '' } as Record<string, string>)} onChange={onPickDir} />
      </div>
      <div className="files-bar">
        <input className="files-filter" placeholder={t('files.filterPh')} value={filter}
          onChange={e => setFilter(e.target.value)} />
        <span className="hint">{filter ? t('files.countOf', { f: filtered.length, t: entries.length }) : t('files.count', { n: entries.length })}</span>
        <span className="spacer" />
        <span className="hint">{t('files.dragHint')}</span>
      </div>
      {error && <p className="hint error-text">{error}</p>}

      {/* 批量操作栏：勾选 ≥1 项时出现 */}
      {checkedEntries.length > 0 && (
        <div className="files-batchbar">
          <span className="bb-count">{t('files.selected', { n: checkedEntries.length })}</span>
          <span className="hint">
            {t('files.dirFileCount', { d: checkedEntries.filter(e => e.dir).length, f: checkedEntries.filter(e => !e.dir).length })}
          </span>
          <button className="btn tiny ghost" onClick={selectAll} disabled={allChecked}>{t('files.selectAll')}</button>
          <button className="btn tiny ghost" onClick={invertSelection}>{t('files.invert')}</button>
          <button className="btn tiny ghost" onClick={clearSelection}>{t('files.clearSel')}</button>
          <span className="spacer" />
          <button className="btn tiny" disabled={batchBusy} onClick={batchDownload}>{t('files.downloadN', { n: checkedEntries.length })}</button>
          <button className="btn tiny danger" disabled={batchBusy} onClick={batchDelete}>
            {batchBusy ? t('files.deleting') : t('files.deleteN', { n: checkedEntries.length })}
          </button>
        </div>
      )}

      {xfers.length > 0 && (
        <div className="files-xfer">
          <div className="files-xfer-head">
            <span>{t('files.transfers')}{activeXfers > 0 ? t('files.inProgress', { n: activeXfers }) : t('files.completed')}</span>
            <span className="hint">{fmtSize(totalLoaded)} / {fmtSize(totalSize)}</span>
            <button className="btn tiny ghost" onClick={() => setXfers([])}>{t('common.clear')}</button>
          </div>
          {xfers.map(x => (
            <div key={x.id} className={'files-xfer-row' + (x.status === 'error' ? ' err' : '')}>
              <span className="fx-name" title={x.name}>{x.name}</span>
              <div className="fx-bar">
                <div className="fx-fill" style={{
                  width: `${x.size ? Math.min(100, x.loaded / x.size * 100) : (x.status === 'done' ? 100 : 0)}%`,
                  background: x.status === 'error' ? 'var(--danger)' : x.status === 'done' ? 'var(--primary)' : undefined,
                }} />
              </div>
              <span className="fx-info">
                {x.status === 'error' ? x.error : x.status === 'done' ? t('files.xferDone') : fmtSize(x.loaded)}
              </span>
            </div>
          ))}
        </div>
      )}

      <table className="files-table">
        <thead>
          <tr>
            <th className="col-chk">
              <input type="checkbox" title={t('files.selectAllTip')}
                checked={allChecked}
                ref={el => { if (el) el.indeterminate = checkedEntries.length > 0 && !allChecked }}
                onChange={e => (e.target.checked ? selectAll() : clearSelection())} />
            </th>
            <th>{t('common.name')}</th><th>{t('common.size')}</th><th>{t('common.time')}</th><th>{t('common.actions')}</th>
          </tr>
        </thead>
        <tbody>
          {filtered.map(e => (
            <tr key={e.name} className={checked.has(e.name) ? 'row-checked' : ''}>
              <td className="col-chk">
                <input type="checkbox" checked={checked.has(e.name)}
                  onChange={() => toggleOne(e.name)}
                  onClick={ev => ev.stopPropagation()} />
              </td>
              <td>
                <span className={'file-name' + (e.dir ? ' is-dir' : '')}
                  onClick={() => e.dir && list(joinPath(path, e.name))}>
                  {e.dir ? '📁 ' : '📄 '}{e.name}
                </span>
              </td>
              <td>{e.dir ? '-' : fmtSize(e.size)}</td>
              <td>{fmtTime(e.time)}</td>
              <td className="file-ops">
                <button className="btn tiny ghost" onClick={() => void download(e)}>{e.dir ? t('files.downloadZip') : t('common.download')}</button>
                <button className="btn tiny ghost" onClick={() => rename(e)}>{t('common.rename')}</button>
                <button className="btn tiny danger" onClick={() => remove(e)}>{t('common.delete')}</button>
              </td>
            </tr>
          ))}
          {filtered.length === 0 && !busy && <tr><td colSpan={5} className="hint">{filter ? t('files.noMatch') : t('files.emptyDir')}</td></tr>}
        </tbody>
      </table>
    </div>
  )
}
