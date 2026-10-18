import { create } from 'zustand'
import { api, ensureSession, wsUrl, apiUrl, saveBackendConfig, type BackendConfig, type DeviceInfo, type Health } from './api'

const SELECTED_KEY = 'ADBreeze-selected'

/** 后端问题类型：none = 正常；unreachable = 不可达；auth = 未登录/会话失效 */
export type BackendIssue = 'none' | 'unreachable' | 'auth'

interface Store {
  devices: DeviceInfo[]
  selected: string | null // 当前操作的设备
  serverConnected: boolean // 事件通道是否在线
  version: string
  panel: string // 激活的功能面板
  /** 后端可达性：false = 最近一次连接尝试失败 */
  backendReachable: boolean
  /** 后端问题类型（驱动 Shell 的提示横幅） */
  backendIssue: BackendIssue
  /** 后端配置变更计数：自增即重建事件通道 */
  backendRevision: number
  /** 外网访问（Cloudflare Tunnel）是否在运行：顶栏徽章数据源；
   *  登录后查一次，之后由设置页的开关操作直接写入（不再轮询） */
  tunnelOn: boolean
  setTunnelOn: (on: boolean) => void
  setPanel: (p: string) => void
  select: (serial: string | null) => void
  refresh: () => Promise<void>
  watchEvents: () => void
  connectWireless: (addr: string) => Promise<void>
  disconnectDevice: (serial: string) => Promise<void>
  forgetDevice: (serial: string) => Promise<void>
  /** 保存后端配置并触发重连 */
  applyBackend: (c: BackendConfig) => void
  /** 登录成功：清除 auth 问题并重建事件通道 */
  loginSucceeded: () => void
  /** 手动重试连接 */
  retryBackend: () => void
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
  backendReachable: false,
  backendIssue: 'none',
  backendRevision: 0,
  tunnelOn: false,
  setTunnelOn: on => set({ tunnelOn: on }),

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

  // 保存后端配置（含手动切换后端地址）并重建事件通道
  applyBackend: c => {
    saveBackendConfig(c)
    set(s => ({ backendRevision: s.backendRevision + 1 }))
  },

  // 登录成功（或已退出后重新登录）：回到正常态并重建事件通道
  loginSucceeded: () => set(s => ({ backendIssue: 'none', backendReachable: true, backendRevision: s.backendRevision + 1 })),

  retryBackend: () => set(s => ({ backendRevision: s.backendRevision + 1 })),
}))

// 安排一次退避重连（事件通道断线 / 后端暂时不可达共用）
function scheduleReconnect(set: (partial: Partial<Store>) => void, get: () => Store) {
  if (stopped) return
  reconnectTimer = setTimeout(() => void openEvents(set, get), reconnectDelay)
  reconnectDelay = Math.min(reconnectDelay * 2, 15000)
}

async function openEvents(
  set: (partial: Partial<Store>) => void,
  get: () => Store,
) {
  const seq = ++eventSeq
  if (evtSocket) evtSocket.close()
  // WS 握手发不了自定义头，先确保会话有效（token / cookie）
  const st = await ensureSession()
  if (seq !== eventSeq) return // 期间已有更新的连接尝试，放弃本次
  if (st === 'unreachable') {
    // 后端不可达：绝不刷新页面——那会把「后端连不上」误判成「凭证失效」，
    // 造成无限刷新循环，用户永远进不了配置界面。改为置位问题类型并按退避
    // 重试，由 Shell 提示检查网络或手动配置后端地址。
    set({ serverConnected: false, backendReachable: false, backendIssue: 'unreachable' })
    scheduleReconnect(set, get)
    return
  }
  if (st === 'unauthorized') {
    // 未登录 / 会话过期（同源跨源一律如此）：不再重试，等登录成功
    // （revision 自增）后重连。Shell 据 backendIssue 切到登录界面。
    set({ serverConnected: false, backendReachable: false, backendIssue: 'auth' })
    return
  }
  set({ backendReachable: true, backendIssue: 'none' })
  const ws = new WebSocket(wsUrl('/ws/events'))
  evtSocket = ws
  ws.onopen = () => {
    reconnectDelay = 1000
    set({ serverConnected: true, backendReachable: true, backendIssue: 'none' })
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
    scheduleReconnect(set, get)
  }
}

// 初始化时拉取版本信息（未鉴权失败则静默）
export async function loadHealth(): Promise<Health | null> {
  try {
    return await fetch(apiUrl('/api/health')).then(r => r.json())
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
