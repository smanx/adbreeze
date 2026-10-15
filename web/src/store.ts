import { create } from 'zustand'
import { api, ensureSession, type DeviceInfo, type Health } from './api'

const SELECTED_KEY = 'ADBreeze-selected'

interface Store {
  devices: DeviceInfo[]
  selected: string | null // 当前操作的设备
  serverConnected: boolean // 事件通道是否在线
  version: string
  panel: string // 激活的功能面板
  setPanel: (p: string) => void
  select: (serial: string | null) => void
  refresh: () => Promise<void>
  watchEvents: () => void
  connectWireless: (addr: string) => Promise<void>
  disconnectDevice: (serial: string) => Promise<void>
  forgetDevice: (serial: string) => Promise<void>
}

let evtSocket: WebSocket | null = null
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let reconnectDelay = 1000
let stopped = false // 主动退出后停止重连
let eventSeq = 0 // 连接代次，避免并发重连产生多余连接

export const useStore = create<Store>((set, get) => ({
  devices: [],
  selected: localStorage.getItem(SELECTED_KEY) || null,
  serverConnected: false,
  version: '',
  panel: 'overview',

  setPanel: p => set({ panel: p }),
  select: serial => {
    if (serial) localStorage.setItem(SELECTED_KEY, serial)
    else localStorage.removeItem(SELECTED_KEY)
    set({ selected: serial })
  },

  refresh: async () => {
    const devices = await api<DeviceInfo[]>('GET', '/api/devices')
    set({ devices })
    // 当前选中设备完全从列表消失时取消选中；offline 设备保留选中（便于重连后恢复）
    const sel = get().selected
    if (sel && !devices.some(d => d.serial === sel)) {
      get().select(null)
    }
  },

  // 设备事件通道：先换取会话 cookie 再连 WS，断线指数退避重连，
  // 重连成功后立即刷新设备列表。
  watchEvents: () => {
    stopped = false
    void openEvents(set, get)
  },

  connectWireless: async addr => {
    await api('POST', '/api/devices/connect', { address: addr })
    await get().refresh()
  },

  disconnectDevice: async serial => {
    await api('DELETE', '/api/devices/' + encodeURIComponent(serial))
    await get().refresh()
  },

  // 忘记掉线设备：从列表记忆中移除
  forgetDevice: async serial => {
    await api('DELETE', `/api/devices/${encodeURIComponent(serial)}/remember`)
    await get().refresh()
  },
}))

async function openEvents(
  set: (partial: Partial<Store>) => void,
  get: () => Store,
) {
  const seq = ++eventSeq
  if (evtSocket) evtSocket.close()
  // WS 无法触发 Basic 弹窗，握手前先确保会话 cookie 有效
  const ok = await ensureSession()
  if (seq !== eventSeq) return // 期间已有更新的连接尝试，放弃本次
  if (!ok) {
    // 凭证失效：重新加载页面会触发浏览器登录弹窗
    location.reload()
    return
  }
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const ws = new WebSocket(`${proto}://${location.host}/ws/events`)
  evtSocket = ws
  ws.onopen = () => {
    reconnectDelay = 1000
    set({ serverConnected: true })
    get().refresh().catch(() => {})
  }
  ws.onmessage = ev => {
    try {
      const msg = JSON.parse(ev.data)
      if (msg.type === 'devices') set({ devices: msg.devices })
    } catch { /* 忽略坏消息 */ }
  }
  ws.onclose = () => {
    if (evtSocket !== ws) return // 已被新连接替换
    evtSocket = null
    set({ serverConnected: false })
    if (stopped) return
    reconnectTimer = setTimeout(() => void openEvents(set, get), reconnectDelay)
    reconnectDelay = Math.min(reconnectDelay * 2, 15000)
  }
}

// 初始化时拉取版本信息（未鉴权失败则静默）
export async function loadHealth(): Promise<Health | null> {
  try {
    return await fetch('/api/health').then(r => r.json())
  } catch {
    return null
  }
}

export function clearReconnectTimer() {
  stopped = true
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}
