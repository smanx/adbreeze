// ---------- 后端地址（跨源部署时手动配置） ----------
// 默认留空 = 与前端同源：会话 cookie 由浏览器自动携带。
// 填写后端地址（如 http://192.168.1.5:9080）后，所有 API / WS 均打往该后端，
// 会话改用 Authorization: Bearer 头携带（跨站请求不发送 SameSite cookie），
// WS 走 ?session= 查询参数（WebSocket 发不了自定义头）。
// 登录凭证只在前端保存会话 token（12h 过期），管理员密码仅在登录表单里提交一次。
import { t } from './i18n'

const BACKEND_KEY = 'ADBreeze-backend'
const TOKEN_KEY = 'ADBreeze-token'
export interface BackendConfig { base: string }
const DEFAULT_BACKEND: BackendConfig = { base: '' }

export function loadBackendConfig(): BackendConfig {
  try {
    const saved = JSON.parse(localStorage.getItem(BACKEND_KEY) || '{}') as Partial<BackendConfig>
    // 只取 base：旧版存档里可能残留 user/pass（Basic 时代明文凭证），读入即弃
    return { base: typeof saved.base === 'string' ? saved.base : '' }
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

// 会话 token：登录成功后由 /api/session 响应体下发，localStorage 持久保存
// （跨源时 cookie 不可用，全靠它；同源时与 cookie 等效，存了也无妨）。
let sessionToken = localStorage.getItem(TOKEN_KEY) || ''

function setSessionToken(v: string) {
  sessionToken = v
  try {
    if (v) localStorage.setItem(TOKEN_KEY, v)
    else localStorage.removeItem(TOKEN_KEY)
  } catch { /* 忽略 */ }
}

/** 当前会话 token（调试/排查用） */
export function hasSessionToken(): boolean { return sessionToken !== '' }

/** 当前生效的鉴权头；无 token 返回空对象（同源 cookie 由浏览器自动携带） */
export function authHeaders(): Record<string, string> {
  return sessionToken ? { Authorization: 'Bearer ' + sessionToken } : {}
}

/** API 完整 URL：同源时保持相对路径 */
export function apiUrl(path: string): string {
  const base = backendBase()
  return base ? base + path : path
}

/** WS 完整 URL：携带会话 token（跨源时 cookie 不随 WS 握手发送） */
export function wsUrl(path: string): string {
  const base = isCrossOrigin() ? backendBase() : `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}`
  const u = new URL(base.replace(/^ws/, 'http') + path)
  u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:'
  if (sessionToken) u.searchParams.set('session', sessionToken)
  return u.toString()
}

// 统一 API 封装：同源与跨源都带 Bearer 会话头（同源另有 cookie 兜底）。
// 错误统一抛 Error(data.error)。
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

/** XHR 通用：带鉴权头发请求，body 流式发送（File 直接传给 send，浏览器分块读、内存恒定）。
 *  onProgress 汇报上传字节；不设 timeout——大文件传输不被掐断。 */
function xhrSendBody(
  method: string, url: string, body: File | ArrayBuffer,
  onProgress?: (loaded: number) => void,
): Promise<{ message?: string; output?: string }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open(method, apiUrl(url))
    for (const [k, v] of Object.entries(authHeaders())) xhr.setRequestHeader(k, v)
    xhr.upload.onprogress = ev => { if (ev.lengthComputable) onProgress?.(ev.loaded) }
    xhr.onload = () => {
      const data = (() => { try { return JSON.parse(xhr.responseText) } catch { return {} } })() as { error?: string; message?: string; output?: string }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data)
      else reject(new Error(data.error || 'HTTP ' + xhr.status))
    }
    xhr.onerror = () => reject(new Error(t('common.networkError')))
    xhr.send(body)
  })
}

/** APK 安装：文件字节直接 POST（后端 push 到 /data/local/tmp 后 pm install -r）。
 *  onProgress 汇报推送字节（push 阶段的进度，pm install 阶段无进度）。 */
export function installApk(serial: string, file: File, onProgress?: (loaded: number) => void): Promise<{ message?: string }> {
  return xhrSendBody('POST', `/api/devices/${encodeURIComponent(serial)}/install`, file, onProgress)
}

/** 安装设备上已有的 APK（文件管理器）：直接 pm install -r <path>，不经本机中转 */
export function installApkFromPath(serial: string, devicePath: string): Promise<{ message?: string }> {
  return api('POST', `/api/devices/${encodeURIComponent(serial)}/install/path`, { path: devicePath })
}

/** 带进度上传文件到设备指定路径（走 files PUT，后端流式直写 adb sync，不落盘） */
export function uploadFileProgress(serial: string, file: File, destPath: string, onProgress?: (loaded: number) => void): Promise<void> {
  return xhrSendBody('PUT', `/api/devices/${encodeURIComponent(serial)}/files?path=${encodeURIComponent(destPath)}`, file, onProgress)
    .then(() => undefined)
}

/** 会话检查结果：ok = 可用；unauthorized = 未登录/已过期；unreachable = 后端不可达 */
export type SessionState = 'ok' | 'unauthorized' | 'unreachable'

// 检查/续签会话：已持有效凭证（cookie 或 token）时 /api/session 直接续签新 token。
// 三态返回值让调用方区分「未登录」与「后端根本连不上」——后者绝不能
// 刷新页面或反复猛试（见 store.openEvents）。
export async function ensureSession(): Promise<SessionState> {
  try {
    const res = await fetch(apiUrl('/api/session'), { method: 'POST', headers: authHeaders() })
    if (res.status === 401) return 'unauthorized'
    if (!res.ok) return 'unreachable'
    const j = await res.json().catch(() => null) as { session?: string } | null
    if (j?.session) setSessionToken(j.session)
    return 'ok'
  } catch {
    return 'unreachable'
  }
}

export interface LoginResult { ok: boolean; lockedFor?: number; error?: string }

// 管理员密码登录：POST /api/session {"password": "..."}。
// 成功后保存会话 token；429 表示连续失败被锁定，lockedFor 为建议等待秒数。
export async function login(password: string): Promise<LoginResult> {
  try {
    const res = await fetch(apiUrl('/api/session'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password }),
    })
    const j = await res.json().catch(() => ({})) as { session?: string; error?: string }
    if (res.ok && j.session) {
      setSessionToken(j.session)
      return { ok: true }
    }
    if (res.status === 429) {
      const s = parseInt(res.headers.get('Retry-After') || '', 10)
      return { ok: false, lockedFor: Number.isFinite(s) ? s : 30 }
    }
    return { ok: false, error: j.error }
  } catch (e) {
    return { ok: false, error: (e as Error).message }
  }
}

// 退出登录：清除后端会话 cookie 并删除本地 token——
// 服务端无浏览器凭证缓存一说，退出即真失效。
export async function clearSession(): Promise<void> {
  setSessionToken('')
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
    return { ok: false, error: (e as Error).message || t('common.networkError') }
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
