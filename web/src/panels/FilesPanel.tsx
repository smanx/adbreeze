import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'
import { useStore } from '../store'

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

  const download = (e: FileEntry) => {
    if (!selected) return
    const full = joinPath(path, e.name)
    // 同源导航自动携带 Basic 凭证/会话 cookie，无需再拼 token
    const url = `/api/devices/${encodeURIComponent(selected)}/files/content?path=${encodeURIComponent(full)}` +
      (e.dir ? '&zip=1' : '')
    const a = document.createElement('a')
    a.href = url
    a.download = e.dir ? e.name + '.zip' : e.name
    a.click() // 浏览器原生下载，自带进度条
  }

  const remove = async (e: FileEntry) => {
    if (!confirm(`删除 ${e.dir ? '目录' : '文件'} ${e.name}？${e.dir ? '（目录内容将一并删除）' : ''}`)) return
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
    const msg = `删除选中的 ${checkedEntries.length} 项（${dirs} 个目录、${files} 个文件）？目录内容将一并删除，不可恢复！`
    if (!confirm(msg)) return
    setBatchBusy(true); setError('')
    let failed = 0
    for (const e of checkedEntries) {
      try {
        await api('DELETE', `/api/devices/${encodeURIComponent(selected!)}/files?path=${encodeURIComponent(joinPath(path, e.name))}`)
      } catch { failed++ }
    }
    setBatchBusy(false)
    if (failed) setError(`${failed} 项删除失败`)
    await list(path) // list 会清空选择
  }

  const batchDownload = () => {
    if (!checkedEntries.length) return
    // 逐个触发浏览器原生下载（文件夹走 zip）；间隔 300ms 避免浏览器拦截连续下载
    checkedEntries.forEach((e, i) => setTimeout(() => download(e), i * 300))
  }

  const mkdir = async () => {
    const name = prompt('新目录名称：')
    if (!name) return
    try {
      await api('POST', `/api/devices/${encodeURIComponent(selected!)}/files/mkdir?path=${encodeURIComponent(joinPath(path, name))}`)
      await list(path)
    } catch (err) { setError((err as Error).message) }
  }

  const rename = async (e: FileEntry) => {
    const newName = prompt(`重命名（${e.dir ? '文件夹' : '文件'}）：`, e.name)
    if (!newName || newName === e.name) return
    if (/[\\/]/.test(newName)) { setError('名称不能包含 / 或 \\'); return }
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
    xhr.open('PUT', `/api/devices/${encodeURIComponent(selected!)}/files?path=${encodeURIComponent(joinPath(destDir, file.name))}`)
    xhr.upload.onprogress = ev => {
      if (ev.lengthComputable) {
        setXfers(q => q.map(t => t.id === id ? { ...t, loaded: ev.loaded } : t))
      }
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        setXfers(q => q.map(t => t.id === id ? { ...t, status: 'done', loaded: file.size } : t))
        resolve()
      } else {
        const msg = `HTTP ${xhr.status}`
        setXfers(q => q.map(t => t.id === id ? { ...t, status: 'error', error: msg } : t))
        reject(new Error(msg))
      }
    }
    xhr.onerror = () => {
      setXfers(q => q.map(t => t.id === id ? { ...t, status: 'error', error: '网络错误' } : t))
      reject(new Error('网络错误'))
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
    if (!selected) { setError('请先选择设备'); return }
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

  if (!selected) return <div className="panel"><p className="hint">请先在设备列表中选择设备。</p></div>

  const activeXfers = xfers.filter(x => x.status === 'uploading').length
  const totalLoaded = xfers.reduce((s, x) => s + x.loaded, 0)
  const totalSize = xfers.reduce((s, x) => s + x.size, 0)

  return (
    <div className={`panel files-panel${dragOver ? ' drag-over' : ''}`}
      onDragOver={e => { e.preventDefault(); setDragOver(true) }}
      onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragOver(false) }}
      onDrop={onDrop}>
      <div className="files-bar">
        <button className="btn tiny" onClick={() => list(parentOf(path))} disabled={path === '/'}>↑ 上级</button>
        <input className="files-path" value={path} onChange={e => setPath(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && list(path)} />
        <button className="btn tiny" onClick={() => list(path)} disabled={busy}>刷新</button>
        <button className="btn tiny" onClick={mkdir}>新建目录</button>
        <button className="btn tiny" onClick={() => uploadRef.current?.click()}>上传文件</button>
        <button className="btn tiny" onClick={() => dirUploadRef.current?.click()}>上传文件夹</button>
        <input ref={uploadRef} type="file" multiple style={{ display: 'none' }} onChange={onPickFiles} />
        <input ref={dirUploadRef} type="file" style={{ display: 'none' }}
          {...({ webkitdirectory: '', directory: '' } as Record<string, string>)} onChange={onPickDir} />
      </div>
      <div className="files-bar">
        <input className="files-filter" placeholder="过滤文件/文件夹（模糊匹配）" value={filter}
          onChange={e => setFilter(e.target.value)} />
        <span className="hint">{filter ? `${filtered.length} / ${entries.length} 项` : `${entries.length} 项`}</span>
        <span className="spacer" />
        <span className="hint">支持拖拽文件 / 文件夹到列表上传</span>
      </div>
      {error && <p className="hint error-text">{error}</p>}

      {/* 批量操作栏：勾选 ≥1 项时出现 */}
      {checkedEntries.length > 0 && (
        <div className="files-batchbar">
          <span className="bb-count">已选 {checkedEntries.length} 项</span>
          <span className="hint">
            {checkedEntries.filter(e => e.dir).length} 个目录 · {checkedEntries.filter(e => !e.dir).length} 个文件
          </span>
          <button className="btn tiny ghost" onClick={selectAll} disabled={allChecked}>全选</button>
          <button className="btn tiny ghost" onClick={invertSelection}>反选</button>
          <button className="btn tiny ghost" onClick={clearSelection}>取消选择</button>
          <span className="spacer" />
          <button className="btn tiny" disabled={batchBusy} onClick={batchDownload}>下载（{checkedEntries.length}）</button>
          <button className="btn tiny danger" disabled={batchBusy} onClick={batchDelete}>
            {batchBusy ? '删除中…' : `删除（${checkedEntries.length}）`}
          </button>
        </div>
      )}

      {xfers.length > 0 && (
        <div className="files-xfer">
          <div className="files-xfer-head">
            <span>传输{activeXfers > 0 ? `（${activeXfers} 个进行中）` : '（已完成）'}</span>
            <span className="hint">{fmtSize(totalLoaded)} / {fmtSize(totalSize)}</span>
            <button className="btn tiny ghost" onClick={() => setXfers([])}>清空</button>
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
                {x.status === 'error' ? x.error : x.status === 'done' ? '完成' : fmtSize(x.loaded)}
              </span>
            </div>
          ))}
        </div>
      )}

      <table className="files-table">
        <thead>
          <tr>
            <th className="col-chk">
              <input type="checkbox" title="全选/全不选"
                checked={allChecked}
                ref={el => { if (el) el.indeterminate = checkedEntries.length > 0 && !allChecked }}
                onChange={e => (e.target.checked ? selectAll() : clearSelection())} />
            </th>
            <th>名称</th><th>大小</th><th>修改时间</th><th>操作</th>
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
                <button className="btn tiny ghost" onClick={() => download(e)}>{e.dir ? '下载zip' : '下载'}</button>
                <button className="btn tiny ghost" onClick={() => rename(e)}>重命名</button>
                <button className="btn tiny danger" onClick={() => remove(e)}>删除</button>
              </td>
            </tr>
          ))}
          {filtered.length === 0 && !busy && <tr><td colSpan={5} className="hint">{filter ? '无匹配项' : '空目录'}</td></tr>}
        </tbody>
      </table>
    </div>
  )
}
