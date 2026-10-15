import { create } from 'zustand'
import { api, type DeviceInfo, type Health } from './api'

const TOKEN_KEY = 'ADBreeze-token'
const SELECTED_KEY = 'ADBreeze-selected'

interface Store {
  token: string
  devices: DeviceInfo[]
  selected: string | null // 当前操作的设备
  serverConnected: boolean // 事件通道是否在线
  version: string
  panel: string // 激活的功能面板
  setToken: (t: string) => void
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

export const useStore = create<Store>((set, get) => ({
  token: localStorage.getItem(TOKEN_KEY) || '',
  devices: [],
  selected: localStorage.getItem(SELECTED_KEY) || null,
  serverConnected: false,
  version: '',
  panel: 'overview',

  setToken: t => {
    localStorage.setItem(TOKEN_KEY, t)
    set({ token: t })
  },
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

  // 设备事件通道：断线指数退避重连，重连成功后立即刷新设备列表
  watchEvents: () => {
    if (evtSocket) evtSocket.close()
    const { token } = get()
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/ws/events?token=${encodeURIComponent(token)}`)
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
      if (!get().token) return
      reconnectTimer = setTimeout(() => get().watchEvents(), reconnectDelay)
      reconnectDelay = Math.min(reconnectDelay * 2, 15000)
    }
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

// 初始化时拉取版本信息（未鉴权失败则静默）
export async function loadHealth(): Promise<Health | null> {
  try {
    return await fetch('/api/health').then(r => r.json())
  } catch {
    return null
  }
}

export function clearReconnectTimer() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}
