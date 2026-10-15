/* ADBreeze Service Worker：仅接管静态外壳（HTML/CSS/JS/图标/Font），
   让应用可安装、离线可打开。绝不缓存 /api 与 /ws——
   这些是设备实时数据与实时流，缓存它们会导致看到陈旧状态甚至连接到已失效的会话。 */

const CACHE = 'adbreeze-shell-v1'
// 安装时只需保证入口可用；其余带 hash 的静态产物在 fetch 时按需缓存
const PRECACHE = ['./', './index.html', './manifest.webmanifest', './icons/icon.svg']

self.addEventListener('install', (e) => {
  // 逐个缓存并各自吞掉失败：addAll 是「全成或全败」，
  // 单个资源（如入口页在鉴权就绪前）失败会让整个 install 被拒，SW 永不激活。
  e.waitUntil(
    caches.open(CACHE)
      .then((c) => Promise.all(PRECACHE.map((u) => c.add(u).catch(() => {}))))
      .then(() => self.skipWaiting())
  )
})

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  )
})

function isRealtime(url) {
  return url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws/')
}

self.addEventListener('fetch', (e) => {
  const req = e.request
  if (req.method !== 'GET') return

  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return
  // 实时接口一律直连网络，不进缓存
  if (isRealtime(url)) return

  // 导航请求：网络优先，断网时回退预缓存的外壳（保证离线可打开）。
  // 不在此处写缓存——否则打开 /legacy.html 等其它页面会污染离线外壳。
  if (req.mode === 'navigate') {
    e.respondWith(
      fetch(req).catch(() =>
        caches.match('./index.html').then((r) => r || caches.match('./'))
      )
    )
    return
  }

  // 静态资源：stale-while-revalidate（先用缓存秒开，后台再刷新）
  e.respondWith(
    caches.match(req).then((cached) => {
      const network = fetch(req)
        .then((res) => {
          if (res && res.ok) {
            const copy = res.clone()
            caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {})
          }
          return res
        })
        .catch(() => cached)
      return cached || network
    })
  )
})
