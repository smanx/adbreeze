package com.smanx.adbreeze

import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast

/**
 * 服务控制台（与桌面版形态一致）：
 *   自定义配置（端口 / 管理员密码）→ 点「启动」→ 只启动服务，
 *   不承载任何界面；用户用浏览器打开服务地址操作设备。
 *
 * 本页职责 = 配置输入 + 日志展示 + 启停控制 + HTTP/HTTPS 唤起浏览器 + 中英切换。
 */
class MainActivity : Activity() {

    private lateinit var editPort: EditText
    private lateinit var editHttpsPort: EditText
    private lateinit var editPass: EditText
    private lateinit var btnToggle: Button
    private lateinit var btnOpenHttp: Button
    private lateinit var btnOpenHttps: Button
    private lateinit var btnLang: Button
    private lateinit var subtitleView: TextView
    private lateinit var sectionConfig: TextView
    private lateinit var sectionStatus: TextView
    private lateinit var fieldPassHint: TextView
    private lateinit var logTitle: TextView
    private lateinit var statusView: TextView
    private lateinit var dotView: View
    private lateinit var logView: TextView

    /** 服务日志监听：Service 的 log() 会回调到这里（仅前台时注册）。 */
    private val logListener: (String) -> Unit = { line ->
        runOnUiThread { appendLog(line) }
    }

    /** 服务阶段监听：phase 变化即刻刷新状态栏与圆点。 */
    private val phaseListener: (String) -> Unit = {
        runOnUiThread { syncState() }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        editPort = findViewById(R.id.editPort)
        editHttpsPort = findViewById(R.id.editHttpsPort)
        editPass = findViewById(R.id.editPass)
        btnToggle = findViewById(R.id.btnToggle)
        btnOpenHttp = findViewById(R.id.btnOpenHttp)
        btnOpenHttps = findViewById(R.id.btnOpenHttps)
        btnLang = findViewById(R.id.btnLang)
        subtitleView = findViewById(R.id.subtitleView)
        sectionConfig = findViewById(R.id.sectionConfig)
        sectionStatus = findViewById(R.id.sectionStatus)
        fieldPassHint = findViewById(R.id.fieldPassHint)
        logTitle = findViewById(R.id.logTitle)
        statusView = findViewById(R.id.statusView)
        dotView = findViewById(R.id.dotView)
        logView = findViewById(R.id.logView)

        loadConfig()
        refreshTexts()

        btnLang.setOnClickListener {
            Lang.toggle(getSharedPreferences(ServerService.PREFS, MODE_PRIVATE))
            refreshTexts()
        }
        btnToggle.setOnClickListener { toggleService() }
        btnOpenHttp.setOnClickListener { openPage(secure = false) }
        btnOpenHttps.setOnClickListener { openPage(secure = true) }
    }

    /**
     * 启动/停止合并按钮：未运行 → 启动（红色）；运行中（含启动过程中）→ 停止（灰色）。
     * 启动前校验端口不冲突，停止走 ACTION_STOP 让 Service 自行收尾。
     */
    private fun toggleService() {
        if (running()) {
            startService(Intent(this, ServerService::class.java).setAction(ServerService.ACTION_STOP))
            syncState()
            return
        }
        val http = editPort.text.toString().trim().ifEmpty { "9080" }
        val https = editHttpsPort.text.toString().trim().ifEmpty { "9443" }
        if (http == https) {
            Toast.makeText(this, Lang.t("toast.portSame"), Toast.LENGTH_SHORT).show()
            return
        }
        saveConfig()
        startForegroundService(Intent(this, ServerService::class.java))
        Toast.makeText(this, Lang.t("toast.starting"), Toast.LENGTH_SHORT).show()
        syncState()
    }

    /** 唤起浏览器打开服务地址；secure = HTTPS（端口取 HTTPS 配置）。 */
    private fun openPage(secure: Boolean) {
        runCatching {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(currentUrl(secure))))
        }.onFailure {
            Toast.makeText(this, Lang.t("toast.noBrowser"), Toast.LENGTH_SHORT).show()
        }
    }

    override fun onResume() {
        super.onResume()
        ServerService.logListeners.add(logListener)
        ServerService.phaseListeners.add(phaseListener)
        // 回放已有日志（应用重开 / 从通知回来）
        logView.text = ServerService.logLines.joinToString("\n")
        syncState()
    }

    override fun onPause() {
        ServerService.logListeners.remove(logListener)
        ServerService.phaseListeners.remove(phaseListener)
        super.onPause()
    }

    // ---------- 文案 ----------

    /** 全量重贴当前语言的界面文案（切语言 / 初始化时调用）。 */
    private fun refreshTexts() {
        btnLang.text = Lang.toggleLabel()
        subtitleView.text = Lang.t("app.subtitle")
        sectionConfig.text = Lang.t("section.config")
        sectionStatus.text = Lang.t("section.status")
        logTitle.text = Lang.t("log.title")
        editPort.hint = Lang.t("field.http")
        editHttpsPort.hint = Lang.t("field.https")
        editPass.hint = Lang.t("field.pass")
        fieldPassHint.text = Lang.t("field.passHint")
        // btnToggle 的文案/配色由 syncState() 按运行态切换（启动=红 / 停止=灰）
        btnOpenHttp.text = Lang.t("btn.openHttp")
        btnOpenHttps.text = Lang.t("btn.openHttps")
        syncState()
    }

    // ---------- 状态 ----------

    private fun running() = ServerService.phase != "idle" && ServerService.phase != "error"

    private fun currentUrl(secure: Boolean): String {
        val port = if (secure) {
            editHttpsPort.text.toString().trim().ifEmpty { "9443" }
        } else {
            editPort.text.toString().trim().ifEmpty { "9080" }
        }
        val scheme = if (secure) "https" else "http"
        return "$scheme://127.0.0.1:$port"
    }

    private fun syncState() {
        val on = running()
        editPort.isEnabled = !on
        editHttpsPort.isEnabled = !on
        editPass.isEnabled = !on
        btnOpenHttp.isEnabled = on
        btnOpenHttps.isEnabled = on

        // 合并启停按钮：未运行=启动（红），运行中=停止（灰）
        btnToggle.text = if (on) Lang.t("btn.stop") else Lang.t("btn.start")
        btnToggle.setBackgroundResource(if (on) R.drawable.bg_btn_stop else R.drawable.bg_btn_start)

        // 启动标记：绿点 = 运行中；橙点 = 启动中；红点 = 未启动 / 失败
        val dotColor = when (ServerService.phase) {
            "ready" -> getColor(R.color.green)
            "extract", "adb", "go" -> getColor(R.color.orange)
            else -> getColor(R.color.red)
        }
        (dotView.background as? GradientDrawable)?.setTint(dotColor)

        val port = editPort.text.toString().trim().ifEmpty { "9080" }
        val httpsPort = editHttpsPort.text.toString().trim().ifEmpty { "9443" }
        statusView.text = when (ServerService.phase) {
            "idle" -> Lang.t("status.idle")
            "extract" -> Lang.t("status.extract")
            "adb" -> Lang.t("status.adb")
            "go" -> Lang.t("status.go")
            "ready" -> Lang.t("status.ready", mapOf("http" to port, "https" to httpsPort))
            "error" -> Lang.t(
                "status.error",
                mapOf("msg" to (ServerService.errorMsg ?: Lang.t("common.unknown")))
            )
            else -> ServerService.phase
        }
        // 状态行配色：就绪绿 / 失败红 / 其余常规正文色
        statusView.setTextColor(
            when (ServerService.phase) {
                "ready" -> getColor(R.color.green)
                "error" -> getColor(R.color.red)
                else -> getColor(R.color.text_body)
            }
        )
    }

    // ---------- 配置 ----------

    private fun loadConfig() {
        val p = getSharedPreferences(ServerService.PREFS, MODE_PRIVATE)
        editPort.setText(p.getString(ServerService.KEY_PORT, "9080"))
        editHttpsPort.setText(p.getString(ServerService.KEY_HTTPS_PORT, "9443"))
        editPass.setText(p.getString(ServerService.KEY_PASS, "admin"))
    }

    private fun saveConfig() {
        getSharedPreferences(ServerService.PREFS, MODE_PRIVATE).edit()
            .putString(ServerService.KEY_PORT, editPort.text.toString().trim().ifEmpty { "9080" })
            .putString(ServerService.KEY_HTTPS_PORT, editHttpsPort.text.toString().trim().ifEmpty { "9443" })
            .putString(ServerService.KEY_PASS, editPass.text.toString().ifEmpty { "admin" })
            .apply()
    }

    @SuppressLint("SetTextI18n")
    private fun appendLog(line: String) {
        logView.append(if (logView.text.isEmpty()) line else "\n$line")
        // 只保留最近 ~32KB，防长跑撑爆 TextView
        val s = logView.text
        if (s.length > 64 * 1024) {
            val cut = s.indexOf('\n', s.length - 32 * 1024).coerceAtLeast(0)
            logView.text = s.subSequence(cut + 1, s.length)
        }
        scrollLogToBottom()
    }

    /** logView 现在嵌在日志卡片的容器里，向上找到 ScrollView 再滚到底。 */
    private fun scrollLogToBottom() {
        var p: android.view.ViewParent? = logView.parent
        while (p != null && p !is ScrollView) p = p.parent
        (p as? ScrollView)?.fullScroll(android.view.View.FOCUS_DOWN)
    }
}
