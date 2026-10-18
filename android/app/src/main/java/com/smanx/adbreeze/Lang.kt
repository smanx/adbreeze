package com.smanx.adbreeze

/**
 * 界面国际化：中英双语，单一事实源（与 web/src/i18n.ts 同思路）。
 *
 * 设计要点：
 *   - 每条消息一个 (zh, en) 元组：杜绝两份词典键漂移。
 *   - 插值：t("key", mapOf("name" to "x")) 替换字符串中的 {name}。
 *   - 持久化：SharedPreferences(PREFS) 的 "lang" 键；
 *     首次启动跟随系统语言（中文系统 → zh，其余 → en）。
 *   - 切换语言后由 MainActivity.refreshTexts() 全量重贴文本，
 *     ServerService 的通知文案在下次刷新通知时自然生效。
 */
object Lang {

    private const val ZH = "zh"
    private const val EN = "en"

    /** 当前语言；默认 en，App.onCreate / MainActivity.onCreate 时会读偏好纠正。 */
    @Volatile
    var current: String = EN
        private set

    // ---------- 词典（zh, en） ----------

    private val messages: Map<String, Pair<String, String>> = mapOf(
        // 应用
        "app.subtitle" to ("安卓服务控制台" to "Android service console"),

        // 分区标签
        "section.config" to ("服务配置" to "Configuration"),
        "section.status" to ("运行状态" to "Status"),
        "log.title" to ("服务日志" to "Service log"),

        // 表单
        "field.http" to ("HTTP 端口（默认 9080）" to "HTTP port (default 9080)"),
        "field.https" to ("HTTPS 端口（默认 9443，须与 HTTP 不同）" to
            "HTTPS port (default 9443, must differ from HTTP)"),
        "field.pass" to ("管理员密码（默认 admin）" to "Admin password (default admin)"),
        "field.passHint" to ("服务启动后配置不可修改" to
            "Configuration is locked while the service is running"),

        // 按钮
        "btn.start" to ("启动" to "Start"),
        "btn.stop" to ("停止" to "Stop"),
        "btn.openHttp" to ("打开 HTTP" to "Open HTTP"),
        "btn.openHttps" to ("打开 HTTPS" to "Open HTTPS"),

        // 状态行
        "status.idle" to ("服务未运行" to "Service stopped"),
        "status.extract" to ("启动中：解压运行资产…" to "Starting: extracting assets…"),
        "status.adb" to ("启动中：启动 adb server…" to "Starting: adb server…"),
        "status.go" to ("启动中：启动服务…" to "Starting: service…"),
        "status.ready" to (
            "运行中：http://127.0.0.1:{http} · https://127.0.0.1:{https}\n局域网访问 http://<本机IP>:{http}"
                to "Running: http://127.0.0.1:{http} · https://127.0.0.1:{https}\nLAN access: http://<device IP>:{http}"),
        "status.error" to ("启动失败：{msg}" to "Failed to start: {msg}"),

        // 气泡提示
        "toast.portSame" to ("HTTP 与 HTTPS 端口不能相同" to
            "HTTP and HTTPS ports must differ"),
        "toast.starting" to ("服务启动中…" to "Starting service…"),
        "toast.noBrowser" to ("没有可用的浏览器" to "No browser available"),

        // 通知
        "notify.channel" to ("ADBreeze 服务" to "ADBreeze service"),
        "notify.starting" to ("服务启动中…" to "Starting service…"),
        "notify.running" to ("运行中：http://127.0.0.1:{port}" to "Running: http://127.0.0.1:{port}"),
        "notify.lan" to ("局域网：{url}" to "LAN: {url}"),
        "notify.failed" to ("启动失败：{msg}" to "Failed to start: {msg}"),
        "notify.stop" to ("停止服务" to "Stop service"),

        // 通用
        "common.unknown" to ("未知错误" to "unknown error"),
    )

    // ---------- API ----------

    /** 读持久化语言；无记录时跟随系统语言。 */
    fun init(prefs: android.content.SharedPreferences) {
        current = prefs.getString(KEY_LANG, null) ?: defaultLang()
    }

    private fun defaultLang(): String =
        if (java.util.Locale.getDefault().language.lowercase().startsWith(ZH)) ZH else EN

    /** 切换语言并持久化，返回新语言。 */
    fun toggle(prefs: android.content.SharedPreferences): String {
        current = if (current == ZH) EN else ZH
        prefs.edit().putString(KEY_LANG, current).apply()
        return current
    }

    fun isZh() = current == ZH

    /** 切换按钮上显示当前语言的短标签（文/EN），占位小不挤控制台布局。 */
    fun toggleLabel(): String = if (isZh()) "文" else "EN"

    /** 取文案；args 用于替换 {key} 占位符。 */
    fun t(key: String, args: Map<String, String> = emptyMap()): String {
        val (zh, en) = messages[key] ?: return key
        var s = if (isZh()) zh else en
        for ((k, v) in args) s = s.replace("{$k}", v)
        return s
    }

    const val KEY_LANG = "lang"
}
