import { useStore } from './store'

// 统一 API 封装：token 走 X-Auth-Token 头，错误统一抛 Error(data.error)
export async function api<T = unknown>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    headers: {
      'X-Auth-Token': useStore.getState().token,
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error((data as { error?: string }).error || 'HTTP ' + res.status)
  return data as T
}

export interface DeviceInfo {
  serial: string
  state: string // device / offline / unauthorized / ...
  model?: string
}

export interface Health {
  version: string
}
