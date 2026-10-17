package com.smanx.adbreeze

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import java.io.File
import java.net.Inet4Address
import java.net.NetworkInterface
import java.util.Collections
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

/**
 * 前台服务：与桌面版形态一致，只负责启动并保活一个服务进程
 * （adb server + Go 服务），不承载任何界面。
 *
 * 启动时序：
 *   ① 解压 assets（adbreeze / adb / 依赖 so）到 filesDir
 *   ② linker 执行 `adb server nodaemon`（前台常驻，无内部 exec，
 *      绕开 Android 10+ 的 W^X exec 限制）→ 轮询 127.0.0.1:5037
 *   ③ linker 执行 adbreeze（Go 服务，配置经环境变量注入，零改动）
 *      → 轮询 HTTP 端口 → phase = ready
 *   ④ 看护协程：任一进程/端口掉线自动重拉
 *
 * 配置来自 SharedPreferences（MainActivity 表单写入）：端口 / 管理员密码。
 * 用户用浏览器打开 http://127.0.0.1:<port> 操作设备。
 */
class ServerService : Service() {

    companion object {
        const val CHANNEL_ID = "adbreeze_server"
        const val NOTIFICATION_ID = 1
        const val ACTION_STOP = "com.smanx.adbreeze.STOP"
        const val ADB_PORT = 5037
        const val PREFS = "adbreeze"
        const val KEY_PORT = "port"
        const val KEY_HTTPS_PORT = "https_port"
        const val KEY_PASS = "pass"

        @Volatile private var phase0 = "idle" // idle|extract|adb|go|ready|error

        /**
         * 服务阶段。赋值即广播给已注册的监听者（MainActivity 据此刷新
         * 状态栏/圆点），避免 UI 轮询或「点了按钮才刷新一次」的滞后。
         */
        var phase: String
            get() = phase0
            set(value) {
                phase0 = value
                phaseListeners.forEach { runCatching { it(value) } }
            }

        @Volatile var errorMsg: String? = null
        @Volatile var httpPort: String = "9080"
        @Volatile var httpsPort: String = "9443"

        /** 最近的服务端日志（环形缓冲，界面回放用）。 */
        val logLines = Collections.synchronizedList(ArrayDeque<String>())

        /** 活动界面的日志订阅者（MainActivity 前台时注册）。 */
        val logListeners = CopyOnWriteArrayList<(String) -> Unit>()

        /** 阶段变化订阅者（MainActivity 前台时注册）。 */
        val phaseListeners = CopyOnWriteArrayList<(String) -> Unit>()

        fun log(line: String) {
            val ts = java.text.SimpleDateFormat("HH:mm:ss", java.util.Locale.ROOT)
                .format(java.util.Date())
            val l = "[$ts] $line"
            logLines.add(l)
            while (logLines.size > 300) logLines.removeAt(0)
            android.util.Log.i("ADBreeze", line)
            logListeners.forEach { runCatching { it(l) } }
        }
    }

    private var adbProc: Process? = null
    private var goProc: Process? = null
    private val watcher = AtomicReference<java.util.concurrent.ScheduledExecutorService?>()
    private lateinit var baseDir: File
    private lateinit var adbBin: File
    private lateinit var goBin: File
    private lateinit var libDir: File

    private var cfgPass = "admin"

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        baseDir = filesDir
        adbBin = File(baseDir, "adb/adb")
        libDir = File(baseDir, "adb")
        goBin = File(baseDir, "adbreeze/adbreeze")
        startForegroundWithNotification(Lang.t("notify.starting"))
        Thread { bootstrap() }.start()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }
        return START_STICKY
    }

    override fun onDestroy() {
        watcher.get()?.shutdownNow()
        killAll()
        phase = "idle"
        errorMsg = null
        log("服务已停止")
        super.onDestroy()
    }

    // ---------- 配置 ----------

    private fun loadConfig() {
        val p = getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        httpPort = p.getString(KEY_PORT, "9080")?.trim().takeUnless { it.isNullOrEmpty() } ?: "9080"
        httpsPort = p.getString(KEY_HTTPS_PORT, "9443")?.trim().takeUnless { it.isNullOrEmpty() } ?: "9443"
        cfgPass = p.getString(KEY_PASS, "admin")?.takeUnless { it.isNullOrEmpty() } ?: "admin"
    }

    // ---------- 启动流程 ----------

    private fun bootstrap() {
        try {
            loadConfig()
            log("配置：HTTP 端口 $httpPort，HTTPS 端口 $httpsPort")

            phase = "extract"
            extractAssets()

            phase = "adb"
            if (!ExecUtil.waitPort(ADB_PORT, 800)) startAdbServer()
            check(ExecUtil.waitPort(ADB_PORT, 10_000)) { "adb server 启动失败（5037 未就绪）" }
            log("adb server 就绪 (127.0.0.1:$ADB_PORT)")

            phase = "go"
            startGoServer()
            check(ExecUtil.waitPort(httpPort.toInt(), 20_000)) {
                "Go 服务启动失败（端口 $httpPort 未就绪）"
            }
            phase = "ready"
            log("服务就绪：http://127.0.0.1:$httpPort（用浏览器打开操作设备）")
            log("HTTPS 已按 PC 端默认开启：https://127.0.0.1:$httpsPort（局域网设备硬解投屏需导入 CA，见网页设置页）")
            for (ip in lanIps()) log("局域网访问：http://$ip:$httpPort · https://$ip:$httpsPort")
            startWatcher()
            startForegroundWithNotification(Lang.t("notify.running", mapOf("port" to httpPort)))
        } catch (e: Throwable) {
            errorMsg = e.message ?: e.toString()
            phase = "error" // 先设 errorMsg 再置 error：监听者刷新 UI 时要能读到
            log("启动失败: $errorMsg")
            startForegroundWithNotification(
                Lang.t("notify.failed", mapOf("msg" to (errorMsg ?: Lang.t("common.unknown"))))
            )
        }
    }

    /** 资产解压：带版本戳，内容没变就不重复解。 */
    private fun extractAssets() {
        val am = assets
        val abi = ExecUtil.pickAbi() ?: throw IllegalStateException("设备 ABI 不受支持（需要 arm64-v8a）")

        val manifest = StringBuilder()
        for (top in listOf("adb/$abi", "adbreeze/$abi")) {
            for (name in am.list(top) ?: emptyArray()) {
                val len = am.open("$top/$name").use { it.available().toLong() }
                manifest.append(top).append('/').append(name).append(':').append(len).append('\n')
            }
        }
        val stamp = File(baseDir, ".assets.stamp")
        if (stamp.exists() && stamp.readText() == manifest.toString()) {
            log("资产已就位，跳过解压")
            return
        }

        val adbDir = File(baseDir, "adb").apply { deleteRecursively(); mkdirs() }
        for (name in am.list("adb/$abi") ?: emptyArray()) {
            copyAsset("adb/$abi/$name", File(adbDir, name))
        }
        val goDir = File(baseDir, "adbreeze").apply { deleteRecursively(); mkdirs() }
        copyAsset("adbreeze/$abi/adbreeze", File(goDir, "adbreeze"))

        for (f in listOf(adbBin, goBin)) check(f.setExecutable(true)) { "chmod +x 失败: $f" }
        stamp.writeText(manifest.toString())
        log("资产解压完成 ($abi)")
    }

    private fun copyAsset(assetPath: String, target: File) {
        assets.open(assetPath).use { input ->
            target.outputStream().use { output -> input.copyTo(output, 64 * 1024) }
        }
    }

    /**
     * 拉起 adb server：`adb server nodaemon` 前台常驻（is_server + no_daemon、
     * 非 fork-server 模式 → 不需要 ack reply fd，进程由本服务持有）。
     * 不能用 `adb start-server`：其内部 fork+execv 在 Android 10+ 被 SELinux 拦。
     */
    private fun startAdbServer() {
        log("启动 adb server: ${adbBin.path}")
        adbProc?.destroyForcibly()
        val env = mapOf(
            "HOME" to baseDir.path, // adbkey 落在 $HOME/.android
            "LD_LIBRARY_PATH" to libDir.path,
            "TMPDIR" to cacheDir.path,
        )
        adbProc = ExecUtil.exec(
            adbBin.path,
            listOf("server", "nodaemon"),
            env,
            File(baseDir, "adb.log"),
        )
    }

    /** 拉起 Go 服务（配置经环境变量注入，Go 侧零改动）。 */
    private fun startGoServer() {
        log("启动 Go 服务: ${goBin.path}")
        goProc?.destroyForcibly()
        val env = mapOf(
            "ADBREEZE_ADDR" to ":$httpPort",
            // HTTPS：与 PC 端一致默认开启，端口可配（默认 9443）。
            // 本机回环访问本就是安全上下文，HTTPS 服务于局域网其他设备的浏览器
            // 硬解投屏（需在其系统里信任自签 CA，入口见网页设置页）。
            "ADBREEZE_HTTPS_ADDR" to ":$httpsPort",
            "ADBREEZE_DATA" to baseDir.path,
            "HOME" to baseDir.path, // os.UserConfigDir() 依赖 HOME，保住 settings 持久化
            "ADBREEZE_ADB" to adbBin.path,
            "ADBREEZE_PASSWORD" to cfgPass,
            "TMPDIR" to cacheDir.path,
        )
        goProc = ExecUtil.exec(goBin.path, emptyList(), env)
        val logFile = File(baseDir, "server.log")
        Thread {
            try {
                goProc?.inputStream?.bufferedReader()?.useLines { lines ->
                    logFile.bufferedWriter().use { w ->
                        lines.forEach { line ->
                            log("go: $line")
                            try { w.appendLine(line); w.flush() } catch (_: Exception) {}
                        }
                    }
                }
            } catch (_: Exception) {
                // 服务停止时 destroyForcibly 会中断阻塞中的 readLine（InterruptedIOException /
                // InterruptedException）。这里是正常退出路径：绝不能让异常逃出线程，
                // 否则未捕获异常处理器会按 Android 默认策略杀死整个应用进程。
            }
        }.start()
    }

    // ---------- 看护 ----------

    private fun startWatcher() {
        val svc = Executors.newSingleThreadScheduledExecutor()
        watcher.set(svc)
        svc.scheduleWithFixedDelay({
            try {
                if (phase != "ready") return@scheduleWithFixedDelay
                if (!ExecUtil.waitPort(ADB_PORT, 1500)) {
                    log("[watchdog] adb server 掉线，重新拉起")
                    startAdbServer()
                    if (!ExecUtil.waitPort(ADB_PORT, 10_000)) log("[watchdog] adb server 重拉失败")
                }
                val goAlive = runCatching { goProc?.isAlive == true }.getOrDefault(false)
                if (!goAlive || !ExecUtil.waitPort(httpPort.toInt(), 1500)) {
                    log("[watchdog] Go 服务掉线，重新拉起")
                    startGoServer()
                    if (!ExecUtil.waitPort(httpPort.toInt(), 20_000)) log("[watchdog] Go 服务重拉失败")
                }
            } catch (e: Throwable) {
                log("[watchdog] 异常: ${e.message}")
            }
        }, 15, 15, TimeUnit.SECONDS)
    }

    private fun killAll() {
        runCatching { adbProc?.destroyForcibly() }
        runCatching { goProc?.destroyForcibly() }
    }

    /** 局域网 site-local IPv4（提示用户从其他设备访问）。 */
    private fun lanIps(): List<String> {
        val out = mutableListOf<String>()
        runCatching {
            val en = NetworkInterface.getNetworkInterfaces() ?: return out
            while (en.hasMoreElements()) {
                for (addr in en.nextElement().inetAddresses) {
                    if (addr is Inet4Address && !addr.isLoopbackAddress && addr.isSiteLocalAddress) {
                        out.add(addr.hostAddress ?: "")
                    }
                }
            }
        }
        return out.filter { it.isNotEmpty() }.distinct()
    }

    // ---------- 通知 ----------

    private fun startForegroundWithNotification(text: String) {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        // minSdk 26：通知渠道 API 始终可用，无需 SDK_INT 判断
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, Lang.t("notify.channel"), NotificationManager.IMPORTANCE_LOW)
        )
        val n = buildNotification(text)
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIFICATION_ID, n)
        }
    }

    private fun buildNotification(text: String): Notification {
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, ServerService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("ADBreeze")
            .setContentText(text)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentIntent(open)
            .addAction(0, Lang.t("notify.stop"), stop)
            .setOngoing(true)
            .build()
    }
}
