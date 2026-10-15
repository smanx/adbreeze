import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { useStore } from '../store'

// 布局检查面板：截图 + 控件边框叠加 + 命中拾取 + 层级树 + 盒模型详情（对齐 AYA Layout）。
// 数据：uiautomator dump XML（服务端拉回），截图：已有 /screenshot 端点。
interface UINode {
  cls: string
  rid: string
  text: string
  desc: string
  x: number; y: number; w: number; h: number
  clickable: boolean
  enabled: boolean
  attrs: [string, string][] // 其余属性原样保留
  children: UINode[]
}

const BOOL_ATTRS = new Set(['checkable', 'checked', 'clickable', 'enabled', 'focusable', 'focused',
  'scrollable', 'long-clickable', 'password', 'selected'])
const SKIP_ATTRS = new Set(['index', 'bounds', 'class', 'resource-id', 'text', 'content-desc', 'package', ...BOOL_ATTRS])

// 解析 uiautomator XML（浏览器 DOMParser）
function parseXML(xml: string): { root: UINode | null; rotation: string } {
  let rotation = '0'
  const m = /<hierarchy[^>]*rotation="(\d+)"/.exec(xml)
  if (m) rotation = m[1]
  const doc = new DOMParser().parseFromString(xml, 'text/xml')
  const el = doc.querySelector('node')
  return { root: el ? toNode(el) : null, rotation }
}

function toNode(el: Element): UINode {
  const bounds = el.getAttribute('bounds') || '[0,0][0,0]'
  const bm = /\[(\d+),(\d+)\]\[(\d+),(\d+)\]/.exec(bounds)
  const [l, t, r, b] = bm ? bm.slice(1).map(Number) : [0, 0, 0, 0]
  const attrs: [string, string][] = []
  for (const a of Array.from(el.attributes)) {
    if (!SKIP_ATTRS.has(a.name)) attrs.push([a.name, a.value])
  }
  return {
    cls: el.getAttribute('class') || 'node',
    rid: el.getAttribute('resource-id') || '',
    text: el.getAttribute('text') || '',
    desc: el.getAttribute('content-desc') || '',
    x: l, y: t, w: r - l, h: b - t,
    clickable: el.getAttribute('clickable') === 'true',
    enabled: el.getAttribute('enabled') !== 'false',
    attrs,
    children: Array.from(el.children).filter(c => c.tagName === 'node').map(toNode),
  }
}

// 查找包含点 (px,py) 且面积最小的节点
function hitTest(n: UINode, px: number, py: number, best: { node: UINode | null; area: number }) {
  if (px >= n.x && px < n.x + n.w && py >= n.y && py < n.y + n.h) {
    const area = n.w * n.h
    if (area < best.area) { best.node = n; best.area = area }
  }
  for (const c of n.children) hitTest(c, px, py, best)
}

// 收集全部节点（拍平给树渲染计数用）
function walk(n: UINode, fn: (n: UINode) => void) {
  fn(n)
  n.children.forEach(c => walk(c, fn))
}

export default function LayoutPanel() {
  const selected = useStore(s => s.selected)
  const token = useStore(s => s.token)
  const [root, setRoot] = useState<UINode | null>(null)
  const [xmlRaw, setXmlRaw] = useState('')
  const [shotURL, setShotURL] = useState('')
  const [imgSize, setImgSize] = useState({ w: 0, h: 0 })
  const [sel, setSel] = useState<UINode | null>(null)
  const [showBorder, setShowBorder] = useState(true)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const imgRef = useRef<HTMLImageElement>(null)

  const refresh = useCallback(async () => {
    if (!selected) return
    setLoading(true); setErr(''); setSel(null)
    try {
      // 并发：截图 + dump
      const [shot, dump] = await Promise.all([
        fetch(`/api/devices/${encodeURIComponent(selected)}/screenshot`, {
          headers: { 'X-Auth-Token': token },
        }).then(r => { if (!r.ok) throw new Error('截图失败 HTTP ' + r.status); return r.blob() }),
        api<{ xml: string; rotation: string }>('GET', `/api/devices/${encodeURIComponent(selected)}/ui-dump`),
      ])
      setShotURL(URL.createObjectURL(shot))
      setXmlRaw(dump.xml)
      setRoot(parseXML(dump.xml).root)
    } catch (e) { setErr((e as Error).message) } finally { setLoading(false) }
  }, [selected, token])

  useEffect(() => { setRoot(null); setShotURL(''); refresh() }, [refresh])

  // 截图加载完成后绘制底图 + 叠加
  useEffect(() => {
    const img = imgRef.current
    if (!img || !shotURL) return
    img.onload = () => {
      setImgSize({ w: img.naturalWidth, h: img.naturalHeight })
      draw()
    }
    if (img.complete && img.naturalWidth) {
      setImgSize({ w: img.naturalWidth, h: img.naturalHeight })
      draw()
    }
  }, [shotURL]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { draw() }, [showBorder, sel, root, imgSize]) // eslint-disable-line react-hooks/exhaustive-deps

  const draw = () => {
    const cv = canvasRef.current
    const img = imgRef.current
    if (!cv || !img || !img.naturalWidth) return
    const dpr = window.devicePixelRatio || 1
    const w = img.naturalWidth, h = img.naturalHeight
    if (cv.width !== w * dpr || cv.height !== h * dpr) { cv.width = w * dpr; cv.height = h * dpr }
    const ctx = cv.getContext('2d')!
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)
    ctx.drawImage(img, 0, 0)
    if (showBorder && root) {
      ctx.strokeStyle = 'rgba(15,157,88,0.9)'
      ctx.lineWidth = 2
      const stroke = (n: UINode) => {
        ctx.strokeRect(n.x + 1, n.y + 1, n.w - 2, n.h - 2)
        n.children.forEach(stroke)
      }
      stroke(root)
    }
    if (sel) {
      ctx.fillStyle = 'rgba(147,196,125,0.55)' // AYA 同款选中高亮
      ctx.fillRect(sel.x, sel.y, sel.w, sel.h)
      ctx.strokeStyle = '#0f9d58'
      ctx.lineWidth = 3
      ctx.strokeRect(sel.x, sel.y, sel.w, sel.h)
    }
  }

  const onCanvasClick = (e: React.MouseEvent<HTMLCanvasElement>) => {
    if (!root) return
    const rect = e.currentTarget.getBoundingClientRect()
    const ratio = imgSize.w / rect.width
    const px = Math.round((e.clientX - rect.left) * ratio)
    const py = Math.round((e.clientY - rect.top) * ratio)
    const best = { node: null as UINode | null, area: Infinity }
    hitTest(root, px, py, best)
    setSel(best.node)
  }

  const exportXML = () => {
    if (!xmlRaw) return
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([xmlRaw], { type: 'text/xml' }))
    a.download = 'window_hierarchy.xml'
    a.click()
    URL.revokeObjectURL(a.href)
  }

  if (!selected) {
    return <div className="panel panel-placeholder"><h2>布局</h2><p className="hint">请先在设备列表中选择设备</p></div>
  }

  let nodeCount = 0
  if (root) walk(root, () => nodeCount++)

  return (
    <div className="panel layout-panel">
      <div className="layout-bar">
        <button className="btn" disabled={loading} onClick={refresh}>{loading ? '采集中…' : '刷新布局'}</button>
        <label className="layout-chk">
          <input type="checkbox" checked={showBorder} onChange={e => setShowBorder(e.target.checked)} />
          显示边框
        </label>
        {root && <span className="hint">{nodeCount} 个控件（rotation {xmlRaw.includes('rotation="1"') ? '90°' : '0°'}）</span>}
        <span className="spacer" />
        <button className="btn ghost" disabled={!xmlRaw} onClick={exportXML}>导出 XML</button>
      </div>
      {err && <p className="error-text">{err}</p>}

      <div className="layout-body">
        <div className="layout-shot">
          <canvas ref={canvasRef} className="layout-canvas" onClick={onCanvasClick}
            style={{ aspectRatio: imgSize.h ? `${imgSize.w}/${imgSize.h}` : '9/19' }} />
          <img ref={imgRef} src={shotURL} style={{ display: 'none' }} alt="" />
          {!shotURL && <div className="hint layout-empty">{loading ? '采集中…' : '点击「刷新布局」开始'}</div>}
        </div>

        <div className="layout-tree">
          <h3>层级树</h3>
          <div className="layout-tree-body">
            {root && <TreeNode node={root} sel={sel} onSelect={setSel} depth={0} />}
            {!root && <div className="hint">无数据</div>}
          </div>
        </div>

        <div className="layout-detail">
          <h3>属性</h3>
          {sel ? (
            <div className="layout-detail-body">
              <div className="layout-boxmodel">
                <span className="bm-class">{sel.cls}</span>
                <div className="bm-box">
                  <span className="bm-pos">{sel.x}, {sel.y}</span>
                  <span className="bm-size">{sel.w} × {sel.h}</span>
                  <span className="bm-edges">R {sel.x + sel.w} · B {sel.y + sel.h}</span>
                </div>
              </div>
              <table className="layout-attrs">
                <tbody>
                  <tr><td>resource-id</td><td className="mono">{sel.rid || '—'}</td></tr>
                  <tr><td>text</td><td>{sel.text || '—'}</td></tr>
                  {sel.desc && <tr><td>content-desc</td><td>{sel.desc}</td></tr>}
                  <tr><td>clickable</td><td>{sel.clickable ? '✓' : '✗'}</td></tr>
                  <tr><td>enabled</td><td>{sel.enabled ? '✓' : '✗'}</td></tr>
                  {sel.attrs.map(([k, v]) => <tr key={k}><td>{k}</td><td>{v}</td></tr>)}
                </tbody>
              </table>
            </div>
          ) : <div className="hint">点击截图或树节点查看属性</div>}
        </div>
      </div>
    </div>
  )
}

// 层级树节点：默认折叠到 4 层，点击选中（联动截图高亮）。
function TreeNode({ node, sel, onSelect, depth }: {
  node: UINode; sel: UINode | null; onSelect: (n: UINode) => void; depth: number
}) {
  const [open, setOpen] = useState(depth < 4)
  const short = node.cls.split('.').pop() || node.cls
  const label = node.rid ? node.rid.split('/').pop()! : node.text || node.desc || ''
  const active = sel === node
  return (
    <div className="tree-node">
      <div className={'tree-row' + (active ? ' active' : '')}
        style={{ paddingLeft: depth * 14 + 4 }} onClick={() => onSelect(node)}>
        {node.children.length > 0 ? (
          <button className="tree-toggle" onClick={e => { e.stopPropagation(); setOpen(o => !o) }}>{open ? '▾' : '▸'}</button>
        ) : <span className="tree-toggle leaf">·</span>}
        <span className="tree-tag">{short}</span>
        {label && <span className="tree-label">{label}</span>}
        {node.clickable && <span className="tree-click">⇄</span>}
      </div>
      {open && node.children.map((c, i) => (
        <TreeNode key={i} node={c} sel={sel} onSelect={onSelect} depth={depth + 1} />
      ))}
    </div>
  )
}
