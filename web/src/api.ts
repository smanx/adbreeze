// 统一 API 封装：同源 fetch 由浏览器自动附带 Basic 凭证 / 会话 cookie，
// 无需再手写鉴权头；错误统一抛 Error(data.error)。
export async function api<T = unknown>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error((data as { error?: string }).error || 'HTTP ' + res.status)
  return data as T
}

// 用浏览器已缓存的 Basic 凭证换取 WebSocket 会话 cookie。
// 成功返回 true；返回 401 说明凭证失效（如已在别处退出），返回 false。
export async function ensureSession(): Promise<boolean> {
  try {
    const res = await fetch('/api/session', { method: 'POST' })
    return res.ok
  } catch {
    return false
  }
}

// 清除后端会话 cookie。注意：浏览器缓存的 Basic 凭证无法由脚本清除，
// 彻底退出需关闭标签页或在浏览器端清除站点凭据。
export async function clearSession(): Promise<void> {
  try {
    await fetch('/api/session', { method: 'DELETE' })
  } catch { /* 忽略 */ }
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
