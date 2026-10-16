// ---------- 后端地址（跨源部署时手动配置） ----------
// 默认留空 = 与前端同源：浏览器自动附带 Basic 凭证与会话 cookie，行为与既往一致。
// 填写后端地址（如 http://192.168.1.5:9080）后，所有 API / WS 均打往该后端，
// 并以 Authorization 头发送下面配置的账号密码——跨源 fetch 不会自动带 Basic 凭证，
// SameSite=Lax 的会话 cookie 也不会随跨站请求发送。
const BACKEND_KEY = 'ADBreeze-backend'
export interface BackendConfig { base: string; user: string; pass: string }
const DEFAULT_BACKEND: BackendConfig = { base: '', user: '', pass: '' }

export function loadBackendConfig(): BackendConfig {
  try {
    const saved = JSON.parse(localStorage.getItem(BACKEND_KEY) || '{}') as Partial<BackendConfig>
    return { ...DEFAULT_BACKEND, ...saved }
  } catch {
    return { ...DEFAULT_BACKEND }
  }
}

export function saveBackendConfig(c: BackendConfig) {
  try { localStorage.setItem(BACKEND_KEY, JSON.stringify(c)) } catch { /* 忽略 */ }
}

/** 规范化后端地址：去空白与尾斜杠；空串 = 同源；不是 http(s) URL 返回 null */
export function normalizeBackendBase(raw: string): string | null {
  const s = raw.trim().replace(/\/+$/, '')
  if (!s) return ''
  if (!/^https?:\/\/[^\s]+$/i.test(s)) return null
  return s
}

/** 当前生效的后端地址（非法存档视为同源） */
export function backendBase(): string {
  return normalizeBackendBase(loadBackendConfig().base) ?? ''
}

/** 是否跨源（配置了独立后端地址） */
export function isCrossOrigin(): boolean { return backendBase() !== '' }

// 跨源会话 token：ensureSession 从 /api/session 响应体换取
// （cookie 不随跨站请求发送，WS 改用 ?session= 查询参数，见后端 WSMiddleware）
let sessionToken = ''

/** 跨源时的 Authorization 头值；同源返回 null（走浏览器自动凭证） */
export function authHeaderValue(): string | null {
  const c = loadBackendConfig()
  if (!isCrossOrigin() || !c.user) return null
  // btoa 只接受 Latin1：非 ASCII 账号密码先按 UTF-8 编码再 base64
  return 'Basic ' + btoa(unescape(encodeURIComponent(c.user + ':' + c.pass)))
}

export function authHeaders(): Record<string, string> {
  const v = authHeaderValue()
  return v ? { Authorization: v } : {}
}

/** API 完整 URL：同源时保持相对路径 */
export function apiUrl(path: string): string {
  const base = backendBase()
  return base ? base + path : path
}

/** WS 完整 URL：跨源时追加 ?session=<token> */
export function wsUrl(path: string): string {
  if (!isCrossOrigin()) {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    return `${proto}://${location.host}${path}`
  }
  const u = new URL(backendBase())
  u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:'
  const sep = path.includes('?') ? '&' : '?'
  const q = sessionToken ? `${sep}session=${encodeURIComponent(sessionToken)}` : ''
  return `${u.protocol}//${u.host}${path}${q}`
}

// 统一 API 封装：同源 fetch 由浏览器自动附带 Basic 凭证 / 会话 cookie，
// 无需再手写鉴权头；跨源时带 Authorization 头。错误统一抛 Error(data.error)。
export async function api<T = unknown>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(apiUrl(url), {
    method,
    headers: { ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}), ...authHeaders() },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error((data as { error?: string }).error || 'HTTP ' + res.status)
  return data as T
}

/** 会话换取结果：ok = 可用；unauthorized = 凭证被拒；unreachable = 后端不可达 */
export type SessionState = 'ok' | 'unauthorized' | 'unreachable'

// 用 Basic 凭证换取 WebSocket 会话（同源走 cookie，跨源经响应体拿 token）。
// 三态返回值让调用方区分「凭证失效」与「后端根本连不上」——后者绝不能
// 刷新页面（会被误解成凭证失效造成无限刷新，见 store.openEvents）。
export async function ensureSession(): Promise<SessionState> {
  try {
    const res = await fetch(apiUrl('/api/session'), { method: 'POST', headers: authHeaders() })
    if (res.status === 401) return 'unauthorized'
    if (!res.ok) return 'unreachable'
    if (isCrossOrigin()) {
      const j = await res.json().catch(() => null) as { session?: string } | null
      sessionToken = j?.session || ''
    }
    return 'ok'
  } catch {
    return 'unreachable'
  }
}

// 清除后端会话 cookie。注意：浏览器缓存的 Basic 凭证无法由脚本清除，
// 彻底退出需关闭标签页或在浏览器端清除站点凭据。
export async function clearSession(): Promise<void> {
  try {
    await fetch(apiUrl('/api/session'), { method: 'DELETE', headers: authHeaders() })
  } catch { /* 忽略 */ }
}

/** 对指定后端地址探活（/api/health 无需鉴权），用于保存前验证；空地址 = 同源 */
export async function probeBackend(base: string): Promise<{ ok: boolean; version?: string; error?: string }> {
  const norm = normalizeBackendBase(base)
  if (norm === null) return { ok: false, error: '地址格式不正确，需以 http:// 或 https:// 开头' }
  try {
    const res = await fetch((norm || location.origin) + '/api/health')
    if (!res.ok) return { ok: false, error: 'HTTP ' + res.status }
    const j = await res.json().catch(() => ({})) as { version?: string }
    return { ok: true, version: j.version }
  } catch (e) {
    return { ok: false, error: (e as Error).message || '网络错误' }
  }
}

export interface DeviceInfo {
  serial: string
  state: string // device / offline / unauthorized / ...
  model?: string
}

export interface Health {
  version: string
}

// ---------- 运行时设置（ADB 可执行文件路径） ----------

export interface AdbSettings {
  /** 当前生效的路径；空字符串表示「默认（从 PATH 查找）」 */
  path: string
  /** 当前解析出的默认 adb 路径，供界面提示 */
  default: string
  /** 默认路径是否可用（PATH 里有没有 adb） */
  available: boolean
  /** 存档文件位置，便于排查 */
  configFile: string
}

export interface AdbSettingsSaveResult {
  message: string
  path: string
  /** adb server 是否已用新路径成功重启 */
  restarted: boolean
  /** adb version 输出，用于确认切换到了哪个版本 */
  version: string
}
