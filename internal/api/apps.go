package api

import (
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"adbreeze/internal/adb"
)

// 常见应用名映射：设备端无 aapt，应用 label 无法从 shell 直接获取，
// 用高频应用内置映射，其余以包名最后段可读化兜底。
var appLabelMap = map[string]string{
	"com.tencent.mm":            "微信",
	"com.eg.android.AlipayGphone": "支付宝",
	"com.tencent.mobileqq":      "QQ",
	"com.ss.android.ugc.aweme":  "抖音",
	"com.taobao.taobao":         "淘宝",
	"com.xunmeng.pinduoduo":     "拼多多",
	"com.jingdong.app.mall":     "京东",
	"com.smile.gifmaker":        "快手",
	"com.alibaba.android.rimet": "钉钉",
	"com.baidu.searchbox":       "百度",
	"com.baidu.netdisk":         "百度网盘",
	"com.android.chrome":        "Chrome",
	"com.microsoft.emmx":        "Edge",
	"com.telegram.messenger":    "Telegram",
	"org.telegram.messenger":    "Telegram",
	"org.telegram.messenger.web": "Telegram Web",
	"com.twitter.android":       "X",
	"com.whatsapp":              "WhatsApp",
	"com.instagram.android":     "Instagram",
	"com.facebook.katana":       "Facebook",
	"com.spotify.music":         "Spotify",
	"tv.danmaku.bili":           "哔哩哔哩",
	"com.coolapk.market":        "酷安",
	"com.xiaomi.market":         "应用商店",
	"com.heytap.market":         "软件商店",
	"com.android.vending":       "Play 商店",
	"com.netease.cloudmusic":    "网易云音乐",
	"com.kuaishou.nebula":       "快手极速版",
	"com.ss.android.lark":       "飞书",
	"com.alibaba.wireless":      "闲鱼",
	"com.taobao.idlefish":       "闲鱼",
	"com.meituan.android.pt":    "美团",
	"com.sankuai.meituan":       "美团",
	"com.dianping.v1":           "大众点评",
	"com.amap.android.maps":     "高德地图",
	"com.autonavi.minimap":      "高德地图",
	"com.baidu.BaiduMap":        "百度地图",
	"com.deepseek.chat":         "DeepSeek",
	"com.openai.chatgpt":        "ChatGPT",
	"com.xunlei.downloadprovider": "迅雷",
	"com.termux":                "Termux",
	"bin.mt.plus":               "MT管理器",
	"bin.mt.plus.canary":        "MT管理器",
	"com.estrongs.android.pop":  "ES文件浏览器",
	"com.mixplorer":             "MiX文件管理器",
	"com.cherry_ai.cherry_studio_app": "Cherry Studio",
	"xyz.chatboxapp.chatbox":    "Chatbox",
	"com.moonshot.kimichat":     "Kimi",
	"com.larus.nova":            "豆包",
	"com.zhiliaoapp.musically":  "TikTok",
	"mark.via":                  "Via浏览器",
	"com.quark.browser":         "夸克",
	"com.ucmobile":              "UC浏览器",
	"com.tencent.mtt":           "QQ浏览器",
	"com.kmxs.reader":           "七猫小说",
	"com.dragon.read":           "番茄小说",
	"com.hellobike.android":     "哈啰",
	"com.jingyao.easybike":      "哈啰",
	"so.ofo.labofo":             "ofo",
	"com.didikee.iflytek":       "讯飞",
	"com.iflytek.speechsuite":   "讯飞语音",
	"com.netease.gl":            "网易大神",
	"com.netease.mail":          "网易邮箱大师",
	"com.qiyi.video":            "爱奇艺",
	"com.youku.phone":           "优酷",
	"com.tencent.qqlive":        "腾讯视频",
	"com.mgtv.iphone above":     "芒果TV",
	"com.huanchengfly.tieba.post": "贴吧lite",
	"com.baidu.tieba":           "百度贴吧",
	"com.zhihu.android":         "知乎",
	"com.xiaohongshu":           "小红书",
	"com.xingin.xhs":            "小红书",
	"com.wapi.wapicertmanage":   "WAPICert",
	"com.adbkeyboard":           "ADB键盘",
	"com.android.adbkeyboard":   "ADB键盘",
	"com.chinamobile.mcloud":    "和彩云",
	"com.tencent.news":          "腾讯新闻",
	"com.netease.newsreader.activity": "网易新闻",
	"com.sohu.newsclient":       "搜狐新闻",
	"com.tmall.wireless":        "天猫",
	"com.eg.android.AlipayGphone.sys": "支付宝",
	"com.hunantv.imgo.activity": "芒果TV",
	"com.lenovo.leos.cloud":     "联想云",
	"com.phoenix.tv":            "凤凰视频",
	"com.tronlinkpro.wallet":    "TronLink",
	"com.binance.dev":           "币安",
	"com.polymarket.android":    "Polymarket",
	"com.cherryai.cherrystudio_app": "Cherry Studio",
	"com.aihuishou.opt":         "爱回收",
	"notion.id":                 "Notion",
	"app.mihon":                 "Mihon",
	"com.apkpure.aegon":         "APKPure",
	"com.github.android":        "GitHub",
	"eu.darken.sdmse":           "SD Maid Pro",
	"org.fdroid.fdroid":         "F-Droid",
	"com.reqable.android":       "Reqable",
	"com.carriez.flutter_hbb":   "RustDesk",
	"com.oray.sunlogin":         "向日葵",
	"com.ruijie.wifim":          "锐捷WiFi",
	"tech.lolli.toolbox":        "ServerBox",
	"cn.wps.moffice.lite":       "WPS Lite",
	"com.xunmeng.pinduoduo.exp": "拼多多",
}

// appName 返回应用显示名：优先内置映射，否则包名最后段可读化。
func appName(pkg string) string {
	if v, ok := appLabelMap[pkg]; ok {
		return v
	}
	seg := pkg
	if i := strings.LastIndexByte(pkg, '.'); i >= 0 && i+1 < len(pkg) {
		seg = pkg[i+1:]
	}
	if seg == "" {
		return pkg
	}
	// 驼峰/下划线转空格，首字母大写
	seg = strings.ReplaceAll(seg, "_", " ")
	var sb strings.Builder
	for i, r := range seg {
		if i == 0 {
			sb.WriteRune(toUpper(r))
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func toUpper(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - 32
	}
	return r
}

// appCachePerDevice 应用列表缓存：设备 → {列表, 采集时间}。
// dumpsys package 全量约 5 秒，缓存 60 秒内直接复用（操作后由 action 主动失效）。
type appCacheEntry struct {
	apps   []adb.AppEntry
	fetched time.Time
}

var appCache sync.Map // serial → *appCacheEntry

func invalidateAppCache(serial string) {
	appCache.Delete(serial)
}

// handleAppList GET /api/devices/{serial}/apps?refresh=1
// 返回包列表 + 应用名/版本/minSdk/targetSdk/存储占用/系统标记/禁用标记/APK 路径。
// 首次约 7 秒（pm list 四连 1s + dumpsys package 5s + diskstats 0.5s 并发），60 秒缓存。
func (s *Server) handleAppList(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	refresh := r.URL.Query().Get("refresh") == "1"
	if !refresh {
		if v, ok := appCache.Load(serial); ok {
			ce := v.(*appCacheEntry)
			if time.Since(ce.fetched) < 60*time.Second {
				writeJSON(w, http.StatusOK, ce.apps)
				return
			}
		}
	}

	apps, err := s.collectApps(serial)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	appCache.Store(serial, &appCacheEntry{apps: apps, fetched: time.Now()})
	writeJSON(w, http.StatusOK, apps)
}

// collectApps 并发采集：pm list 四连 + dumpsys package 全量段解析 + diskstats 大小数组。
func (s *Server) collectApps(serial string) ([]adb.AppEntry, error) {
	c := s.mgr.Client()

	allCh := make(chan []string, 1)
	sysCh := make(chan []string, 1)
	disCh := make(chan []string, 1)
	pfCh := make(chan map[string]string, 1) // pkg → apk path（-f 输出）
	dumpCh := make(chan string, 1)
	diskCh := make(chan string, 1)
	go func() { allCh <- pkgLines(shellIgnoreErr(c, serial, "pm list packages --user 0")) }()
	go func() { sysCh <- pkgLines(shellIgnoreErr(c, serial, "pm list packages -s --user 0")) }()
	go func() { disCh <- pkgLines(shellIgnoreErr(c, serial, "pm list packages -d --user 0")) }()
	go func() {
		out := shellIgnoreErr(c, serial, "pm list packages -f --user 0")
		paths := map[string]string{}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			// package:/path/to/base.apk=com.example
			if !strings.HasPrefix(line, "package:") {
				continue
			}
			eq := strings.LastIndex(line, "=")
			if eq < 0 {
				continue
			}
			paths[line[eq+1:]] = strings.TrimPrefix(line[:eq], "package:")
		}
		pfCh <- paths
	}()
	go func() { dumpCh <- shellIgnoreErr(c, serial, "dumpsys package") }()
	go func() { diskCh <- shellIgnoreErr(c, serial, "dumpsys diskstats") }()

	sysSet := toSet(<-sysCh)
	disSet := toSet(<-disCh)
	paths := <-pfCh
	meta := parseAllPkgMeta(<-dumpCh)  // pkg → {version, minSdk, targetSdk}
	usage := parseDiskstats(<-diskCh)  // pkg → {app, data, cache}

	pkgs := <-allCh
	sort.Strings(pkgs)
	apps := make([]adb.AppEntry, 0, len(pkgs))
	for _, p := range pkgs {
		m := meta[p]
		u := usage[p]
		apps = append(apps, adb.AppEntry{
			Package:     p,
			Label:       appName(p),
			VersionName: m.version,
			MinSdk:      m.minSdk,
			TargetSdk:   m.targetSdk,
			AppSize:     u.app,
			DataSize:    u.data,
			CacheSize:   u.cache,
			System:      sysSet[p],
			Disabled:    disSet[p],
			ApkPath:     paths[p],
		})
	}
	return apps, nil
}

// ---------- dumpsys package 全量段解析 ----------

type pkgMeta struct {
	version   string
	minSdk    int
	targetSdk int
}

var (
	pkgSectionRe = regexp.MustCompile(`Package \[([^\]]+)\]`)
	verLineRe    = regexp.MustCompile(`versionCode=(\d+)(?:\S*)? minSdk=(\d+) targetSdk=(\d+)`)
	verNameRe    = regexp.MustCompile(`versionName=(\S+)`)
)

// parseAllPkgMeta 从 dumpsys package 全量输出提取每个包的版本信息。
// 段结构："  Package [pkg] (...):" 后跟若干行，其中一行 "    versionCode=N minSdk=M targetSdk=K"，
// 下一行 "    versionName=x.y.z"。段与版本行 1:1 对齐（真机 569/569 验证）。
func parseAllPkgMeta(out string) map[string]pkgMeta {
	meta := map[string]pkgMeta{}
	sections := pkgSectionRe.FindAllStringSubmatchIndex(out, -1)
	for i, loc := range sections {
		end := len(out)
		if i+1 < len(sections) {
			end = sections[i+1][0]
		}
		seg := out[loc[2]:loc[3]]
		_ = end
		body := out[loc[1]:]
		if i+1 < len(sections) {
			body = out[loc[1]:sections[i+1][0]]
		}
		_ = seg
		m := pkgMeta{}
		if vm := verLineRe.FindStringSubmatch(body); vm != nil {
			m.minSdk, _ = strconv.Atoi(vm[2])
			m.targetSdk, _ = strconv.Atoi(vm[3])
		}
		if vn := verNameRe.FindStringSubmatch(body); vn != nil {
			m.version = vn[1]
		}
		meta[seg] = m
	}
	return meta
}

// ---------- dumpsys diskstats 大小数组解析 ----------

type sizeTriple struct {
	app, data, cache uint64
}

// parseDiskstats 解析 diskstats 的四个平行数组（Package Names / App Sizes / App Data Sizes / Cache Sizes），
// 三组大小数组与包名数组按下标一一对应（真机 545/545/545 验证）。
func parseDiskstats(out string) map[string]sizeTriple {
	usage := map[string]sizeTriple{}
	names := extractBracketedList(out, `Package Names: \[([^\]]*)\]`)
	apps := extractNumberList(out, `App Sizes: \[([^\]]*)\]`)
	datas := extractNumberList(out, `App Data Sizes: \[([^\]]*)\]`)
	caches := extractNumberList(out, `Cache Sizes: \[([^\]]*)\]`)
	for i, name := range names {
		var t sizeTriple
		if i < len(apps) {
			t.app = apps[i]
		}
		if i < len(datas) {
			t.data = datas[i]
		}
		if i < len(caches) {
			t.cache = caches[i]
		}
		usage[name] = t
	}
	return usage
}

func extractBracketedList(out, pattern string) []string {
	m := regexp.MustCompile(pattern).FindStringSubmatch(out)
	if m == nil {
		return nil
	}
	inner := m[1]
	var items []string
	for _, s := range strings.Split(inner, `","`) {
		s = strings.Trim(s, `"`)
		if s != "" {
			items = append(items, s)
		}
	}
	return items
}

func extractNumberList(out, pattern string) []uint64 {
	m := regexp.MustCompile(pattern).FindStringSubmatch(out)
	if m == nil {
		return nil
	}
	var nums []uint64
	for _, s := range strings.Split(m[1], ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			nums = append(nums, 0)
			continue
		}
		v, _ := strconv.ParseUint(s, 10, 64)
		nums = append(nums, v)
	}
	return nums
}

// handleAppInfo GET /api/devices/{serial}/apps/info?package=
// 单包详情：dumpsys package 解析（懒加载，约 0.5s）。
func (s *Server) handleAppInfo(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	pkg := sanitizePackageName(r.URL.Query().Get("package"))
	if pkg == "" {
		writeErr(w, http.StatusBadRequest, "需要合法的 package 参数")
		return
	}
	out, err := s.mgr.Client().ShellTimeout(serial, "dumpsys package "+pkg, 15*time.Second)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	info := parsePackageInfo(pkg, out)
	if info == nil {
		writeErr(w, http.StatusNotFound, "未找到该包")
		return
	}
	writeJSON(w, http.StatusOK, *info)
}

var (
	dumpVerLineRe = regexp.MustCompile(`versionCode=(\d+)(?:\S*)? minSdk=(\d+) targetSdk=(\d+)`)
	dumpVerNameRe = regexp.MustCompile(`versionName=(\S+)`)
)

// parsePackageInfo 从 dumpsys package 输出提取关键字段。
func parsePackageInfo(pkg, out string) *adb.AppInfo {
	info := &adb.AppInfo{Package: pkg, Label: appName(pkg), Enabled: true}
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "codePath="):
			if info.ApkPath == "" {
				info.ApkPath = strings.TrimPrefix(t, "codePath=")
			}
		case strings.HasPrefix(t, "versionName="):
			if info.VersionName == "" {
				info.VersionName = strings.TrimPrefix(t, "versionName=")
			}
		case strings.HasPrefix(t, "firstInstallTime="):
			if info.FirstInstall == "" {
				info.FirstInstall = strings.TrimPrefix(t, "firstInstallTime=")
			}
		case strings.HasPrefix(t, "lastUpdateTime="):
			info.LastUpdate = strings.TrimPrefix(t, "lastUpdateTime=")
		case strings.HasPrefix(t, "flags=["):
			info.System = strings.Contains(t, "SYSTEM")
		case strings.HasPrefix(t, "enabled="):
			// enabled=0（DEFAULT）之外的用户态禁用：ENABLED=1 / DISABLED=2 / DISABLED_USER=3...
			if v := strings.TrimPrefix(t, "enabled="); v != "" && v != "0" && v != "1" {
				info.Enabled = false
			}
		}
	}
	if m := dumpVerLineRe.FindStringSubmatch(out); m != nil {
		info.VersionCode, _ = strconv.Atoi(m[1])
		info.MinSdk, _ = strconv.Atoi(m[2])
		info.TargetSdk, _ = strconv.Atoi(m[3])
	}
	if info.VersionName == "" && info.ApkPath == "" {
		return nil // dumpsys 无此包
	}
	return info
}

// handleAppAction POST /api/devices/{serial}/apps/action {package, action}
// action: open / stop / uninstall / clear / disable / enable
func (s *Server) handleAppAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Package string `json:"package"`
		Action  string `json:"action"`
	}
	if !readJSONBody(w, r, &req) {
		return
	}
	pkg := sanitizePackageName(req.Package)
	if pkg == "" {
		writeErr(w, http.StatusBadRequest, "需要合法的 package 参数")
		return
	}
	serial := r.PathValue("serial")
	c := s.mgr.Client()

	var cmd string
	switch req.Action {
	case "open":
		// 解析 launcher activity：resolve-activity 的 brief 输出末行即 component
		activity, err := c.ShellTimeout(serial,
			"cmd package resolve-activity --brief -c android.intent.category.LAUNCHER "+pkg+
				" | tail -1", 10*time.Second)
		if err != nil || strings.TrimSpace(activity) == "" {
			writeErr(w, http.StatusBadGateway, "无法解析启动入口: "+err.Error())
			return
		}
		component := strings.TrimSpace(activity)
		if !strings.Contains(component, "/") {
			component = component + "/"
		}
		out, err := c.ShellTimeout(serial, "am start -n "+component, 10*time.Second)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": strings.TrimSpace(firstLine(out))})
		return
	case "stop":
		cmd = "am force-stop " + pkg
	case "uninstall":
		cmd = "pm uninstall --user 0 " + pkg
	case "clear":
		cmd = "pm clear " + pkg
	case "disable":
		cmd = "pm disable-user --user 0 " + pkg
	case "enable":
		cmd = "pm enable " + pkg
	default:
		writeErr(w, http.StatusBadRequest, "未知操作: "+req.Action)
		return
	}

	out, err := c.ShellTimeout(serial, cmd, 30*time.Second)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	msg := strings.TrimSpace(out)
	// 成功提示映射：pm uninstall/clear 输出 "Success"，am force-stop / pm disable-user 输出为空
	successMsg := map[string]string{
		"stop": "已停止", "uninstall": "已卸载", "clear": "已清除数据",
		"disable": "已禁用", "enable": "已启用",
	}[req.Action]
	if strings.Contains(msg, "Success") || msg == "" {
		msg = successMsg
	}
	invalidateAppCache(serial) // 状态类操作后刷新缓存
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// handleAppApk GET /api/devices/{serial}/apps/apk?package=
// 导出 APK：pm path 拿 base.apk 路径后走 Pull 流式下载。
func (s *Server) handleAppApk(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	pkg := sanitizePackageName(r.URL.Query().Get("package"))
	if pkg == "" {
		writeErr(w, http.StatusBadRequest, "需要合法的 package 参数")
		return
	}
	out, err := s.mgr.Client().ShellTimeout(serial, "pm path "+pkg, 10*time.Second)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	apkPath := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package:") {
			apkPath = strings.TrimPrefix(line, "package:")
			if strings.Contains(apkPath, "base.apk") {
				break // 优先 base.apk
			}
		}
	}
	if apkPath == "" {
		writeErr(w, http.StatusNotFound, "未找到 APK 路径")
		return
	}
	name := pkg + ".apk"
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	if err := s.mgr.Client().Pull(serial, apkPath, w); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
	}
}

// ---------- 工具 ----------

func shellIgnoreErr(c *adb.Client, serial, cmd string) string {
	out, _ := c.ShellTimeout(serial, cmd, 20*time.Second)
	return out
}

func pkgLines(out string) []string {
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package:") {
			name := strings.TrimPrefix(line, "package:")
			// -f 格式 "package:/path=x.y"，普通格式 "package:x.y"
			if eq := strings.LastIndex(name, "="); eq >= 0 {
				name = name[eq+1:]
			}
			if name != "" {
				pkgs = append(pkgs, name)
			}
		}
	}
	return pkgs
}

func toSet(list []string) map[string]bool {
	set := make(map[string]bool, len(list))
	for _, v := range list {
		set[v] = true
	}
	return set
}

// sanitizePackageName 只允许包名合法字符，防 shell 注入。
func sanitizePackageName(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || len(p) > 255 {
		return ""
	}
	for _, ch := range p {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' ||
			ch == '.' || ch == '_' || ch == '-' || ch == '$') {
			return ""
		}
	}
	return p
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
