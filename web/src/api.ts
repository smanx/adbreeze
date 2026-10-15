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
