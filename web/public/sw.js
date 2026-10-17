/* ADBreeze Service Worker：仅接管静态外壳（HTML/CSS/JS/图标/Font），
   让应用可安装、离线可打开。绝不缓存 /api 与 /ws——
   这些是设备实时数据与实时流，缓存它们会导致看到陈旧状态甚至连接到已失效的会话。

   Safari/WebKit 限制（v2 修复）：respondWith 返回带 redirected 标记的响应会被
   直接拒绝（"Response served by service worker has redirections"），Chromium 则容忍。
   而服务端 FileServer 对 /index.html 会 301 到 ./，v1 的预缓存把它跟随重定向后
   存进了缓存——任何路径把这条记录回给页面（离线兜底 / 证书未信任时 fetch 失败
   走兜底）都会在 Safari 上炸。因此：
   1. 预缓存去掉 ./index.html（其内容与 ./ 相同，无需单独缓存）；
   2. 所有 respondWith 前经 clean() 剥掉 redirected 标记；
   3. 只缓存非 redirected 的网络响应；
   4. CACHE 版本号 +1：激活时删掉旧缓存，老设备上的毒缓存自动清空。 */

const CACHE = 'adbreeze-shell-v3'
// 安装时只需保证入口可用；其余带 hash 的静态产物在 fetch 时按需缓存。
// 注意不要放 ./index.html —— 它会被服务端 301（见上）。
const PRECACHE = ['./', './manifest.webmanifest', './icons/icon.svg']

// WebKit 拒绝 redirected 响应：new Response 复制一份即剥掉该标记。
function clean(res) {
  if (!res || !res.redirected) return res
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers: res.headers })
}

self.addEventListener('install', (e) => {
  // 逐个缓存并各自吞掉失败：addAll 是「全成或全败」，
  // 单个资源加载失败会让整个 install 被拒，SW 永不激活。
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
  // 不在此处写缓存——否则其它页面的导航会污染离线外壳。
  if (req.mode === 'navigate') {
    e.respondWith(
      fetch(req)
        .then((res) => clean(res))
        .catch(() =>
          caches.match('./').then((r) => clean(r))
        )
    )
    return
  }

  // 静态资源：stale-while-revalidate（先用缓存秒开，后台再刷新）。
  // 只缓存非 redirected 的响应（重定向入口不进缓存，见文件头说明）。
  e.respondWith(
    caches.match(req).then((cached) => {
      const cleanCached = clean(cached)
      const network = fetch(req)
        .then((res) => {
          if (res && res.ok && !res.redirected) {
            const copy = res.clone()
            caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {})
          }
          return clean(res)
        })
        .catch(() => cleanCached)
      return cleanCached || network
    })
  )
})
