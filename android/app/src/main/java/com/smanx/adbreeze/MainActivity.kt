package com.smanx.adbreeze

import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import android.widget.Toast

/**
 * 服务控制台（与桌面版形态一致）：
 *   自定义配置（端口 / 管理员密码）→ 点「启动」→ 只启动服务，
 *   不承载任何界面；用户用浏览器打开服务地址操作设备。
 *
 * 本页职责 = 配置输入 + 日志展示 + 启停控制 + 一键唤起浏览器。
 */
class MainActivity : Activity() {

    private lateinit var editPort: EditText
    private lateinit var editHttpsPort: EditText
    private lateinit var editPass: EditText
    private lateinit var btnStart: Button
    private lateinit var btnStop: Button
    private lateinit var btnOpen: Button
    private lateinit var statusView: TextView
    private lateinit var dotView: TextView
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
        btnStart = findViewById(R.id.btnStart)
        btnStop = findViewById(R.id.btnStop)
        btnOpen = findViewById(R.id.btnOpen)
        statusView = findViewById(R.id.statusView)
        dotView = findViewById(R.id.dotView)
        logView = findViewById(R.id.logView)

        loadConfig()

        btnStart.setOnClickListener {
            val http = editPort.text.toString().trim().ifEmpty { "9080" }
            val https = editHttpsPort.text.toString().trim().ifEmpty { "9443" }
            if (http == https) {
                Toast.makeText(this, "HTTP 与 HTTPS 端口不能相同", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            saveConfig()
            startForegroundService(Intent(this, ServerService::class.java))
            Toast.makeText(this, "服务启动中…", Toast.LENGTH_SHORT).show()
            syncState()
        }
        btnStop.setOnClickListener {
            startService(Intent(this, ServerService::class.java).setAction(ServerService.ACTION_STOP))
            syncState()
        }
        btnOpen.setOnClickListener {
            runCatching {
                startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(currentUrl())))
            }.onFailure {
                Toast.makeText(this, "没有可用的浏览器", Toast.LENGTH_SHORT).show()
            }
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

    // ---------- 状态 ----------

    private fun running() = ServerService.phase != "idle" && ServerService.phase != "error"

    private fun currentUrl(): String {
        val port = editPort.text.toString().trim().ifEmpty { "9080" }
        return "http://127.0.0.1:$port"
    }

    private fun syncState() {
        val on = running()
        editPort.isEnabled = !on
        editHttpsPort.isEnabled = !on
        editPass.isEnabled = !on
        btnStart.isEnabled = !on
        btnStop.isEnabled = ServerService.phase != "idle"
        btnOpen.isEnabled = on

        // 启动标记：绿点 = 运行中；橙点 = 启动中；红点 = 未启动 / 失败
        val dotColor = when (ServerService.phase) {
            "ready" -> 0xFF4CAF50 // 绿
            "extract", "adb", "go" -> 0xFFFFA000 // 橙
            else -> 0xFFE53935 // 红
        }.toInt()
        dotView.setTextColor(dotColor)

        val port = editPort.text.toString().trim().ifEmpty { "9080" }
        val httpsPort = editHttpsPort.text.toString().trim().ifEmpty { "9443" }
        statusView.text = when (ServerService.phase) {
            "idle" -> "服务未运行"
            "extract" -> "启动中：解压运行资产…"
            "adb" -> "启动中：启动 adb server…"
            "go" -> "启动中：启动服务…"
            "ready" -> "运行中：http://127.0.0.1:$port · https://127.0.0.1:$httpsPort" +
                "（局域网访问 http://<本机IP>:$port）"
            "error" -> "启动失败：${ServerService.errorMsg ?: "未知错误"}"
            else -> ServerService.phase
        }
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
        (logView.parent as? android.widget.ScrollView)?.fullScroll(android.view.View.FOCUS_DOWN)
    }
}
