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
import java.util.Collections
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

/**
 * 前台服务：负责 adb server 与 Go 服务子进程的完整生命周期。
 *
 * 启动时序（详见根目录 ANDROID.md）：
 *   ① 解压 assets（adbreeze / adb / 依赖 so / libtermux-exec）到 filesDir
 *   ② linker64 执行 adb start-server（LD_PRELOAD 钩子让 adb 内部的
 *      fork-server execv 也走 linker）→ 轮询 127.0.0.1:5037
 *   ③ linker64 执行 adbreeze（Go 服务，环境变量注入全部配置）
 *      → 轮询 127.0.0.1:9080 → phase = ready
 *   ④ 看护协程：5037 / 9080 掉了自动重拉
 *
 * 关键点：Go 的 os/exec 走裸 execve syscall，LD_PRELOAD 钩不到它，所以
 * adb server 必须由壳层前置拉起——Go 的 Client.Ping() 直接成功，永远
 * 走不到它内部被 SELinux 拦掉的 exec 分支。
 */
class ServerService : Service() {

    companion object {
        const val CHANNEL_ID = "adbreeze_server"
        const val NOTIFICATION_ID = 1
        const val ACTION_STOP = "com.smanx.adbreeze.STOP"

        const val HTTP_PORT = 9080
        const val ADB_PORT = 5037

        /** 供 MainActivity 轮询的全局状态。 */
        @Volatile var phase: String = "idle" // idle|extract|adb|go|ready|error
        @Volatile var errorMsg: String? = null

        /** 最近的服务端输出（环形缓冲，错误页展示用）。 */
        val logLines = Collections.synchronizedList(ArrayDeque<String>())

        fun log(line: String) {
            val ts = java.text.SimpleDateFormat("HH:mm:ss", java.util.Locale.ROOT)
                .format(java.util.Date())
            val l = "[$ts] $line"
            logLines.add(l)
            while (logLines.size > 300) logLines.removeAt(0)
            android.util.Log.i("ADBreeze", line)
        }
    }

    private var adbProc: Process? = null
    private var goProc: Process? = null
    private val watcher = AtomicReference<java.util.concurrent.ScheduledExecutorService?>()
    private lateinit var filesDir_: File
    private lateinit var adbBin: File
    private lateinit var goBin: File
    private lateinit var libDir: File

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        filesDir_ = filesDir
        adbBin = File(filesDir_, "adb/adb")
        libDir = File(filesDir_, "adb")
        goBin = File(filesDir_, "adbreeze/adbreeze")
        startForegroundWithNotification()
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
        if (phase != "error") phase = "idle"
        super.onDestroy()
    }

    // ---------- 启动流程 ----------

    private fun bootstrap() {
        try {
            phase = "extract"
            extractAssets()

            phase = "adb"
            if (!ExecUtil.waitPort(ADB_PORT, 800)) startAdbServer()
            check(ExecUtil.waitPort(ADB_PORT, 10_000)) { "adb server 启动失败（5037 未就绪）" }
            log("adb server 就绪 (127.0.0.1:$ADB_PORT)")

            phase = "go"
            startGoServer()
            check(ExecUtil.waitPort(HTTP_PORT, 20_000)) { "Go 服务启动失败（$HTTP_PORT 未就绪）" }
            log("Go 服务就绪 (http://127.0.0.1:$HTTP_PORT)")

            phase = "ready"
            startWatcher()
        } catch (e: Throwable) {
            phase = "error"
            errorMsg = e.message ?: e.toString()
            log("启动失败: $errorMsg")
            updateNotification("启动失败：$errorMsg")
        }
    }

    /** 资产解压：带版本戳，内容没变就不重复解。 */
    private fun extractAssets() {
        val am = assets
        val abi = ExecUtil.pickAbi() ?: throw IllegalStateException("设备 ABI 不受支持（需要 arm64-v8a 或 x86_64）")

        val manifest = StringBuilder()
        for (top in listOf("adb/$abi", "adbreeze/$abi")) {
            for (name in am.list(top) ?: emptyArray()) {
                val len = am.open("$top/$name").use { it.available().toLong() }
                manifest.append(top).append('/').append(name).append(':').append(len).append('\n')
            }
        }
        val stamp = File(filesDir_, ".assets.stamp")
        if (stamp.exists() && stamp.readText() == manifest.toString()) {
            log("资产已就位，跳过解压")
            return
        }

        // adb：adb 二进制 + 全部依赖 so 平铺在同一目录（LD_LIBRARY_PATH 指向它）
        val adbDir = File(filesDir_, "adb").apply { deleteRecursively(); mkdirs() }
        for (name in am.list("adb/$abi") ?: emptyArray()) {
            copyAsset("adb/$abi/$name", File(adbDir, name))
        }
        // Go 服务端
        val goDir = File(filesDir_, "adbreeze").apply { deleteRecursively(); mkdirs() }
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
     *
     * 不能用 `adb start-server`：其内部 fork+execv 拉起 fork-server 子进程——
     * Android 10+ 下被 SELinux 拦（LD_PRELOAD 钩子也救不了 Go 侧裸 syscall exec，
     * 且 x86 主机上看门钩子重定向的 /system/bin/linker64 是 x86 版，跑不了
     * arm64 ELF）。server nodaemon 全程无内部 exec，两个平台通吃。
     */
    private fun startAdbServer() {
        log("启动 adb server: ${adbBin.path}")
        adbProc?.destroyForcibly()
        val env = mapOf(
            "HOME" to filesDir_.path, // adbkey 落在 $HOME/.android
            "LD_LIBRARY_PATH" to libDir.path,
            "TMPDIR" to cacheDir.path,
        )
        adbProc = ExecUtil.exec(
            adbBin.path,
            listOf("server", "nodaemon"),
            env,
            File(filesDir_, "adb.log"),
        )
    }

    /** 拉起 Go 服务（环境变量注入 = 零改动的配置通道）。 */
    private fun startGoServer() {
        log("启动 Go 服务: ${goBin.path}")
        goProc?.destroyForcibly()
        val env = mapOf(
            "ADBREEZE_ADDR" to ":$HTTP_PORT",
            "ADBREEZE_HTTPS" to "false", // 127.0.0.1 本身是安全上下文，WebCodecs 可用
            "ADBREEZE_DATA" to filesDir_.path,
            "HOME" to filesDir_.path, // os.UserConfigDir() 依赖 HOME，保住 settings 持久化
            "ADBREEZE_ADB" to adbBin.path,
            "ADBREEZE_USER" to "admin",
            "ADBREEZE_PASSWORD" to "admin",
            "TMPDIR" to cacheDir.path,
        )
        goProc = ExecUtil.exec(goBin.path, emptyList(), env)
        // 输出泵：写日志文件 + 环形缓冲（错误页展示）
        val logFile = File(filesDir_, "server.log")
        Thread {
            goProc?.inputStream?.bufferedReader()?.useLines { lines ->
                logFile.bufferedWriter().use { w ->
                    lines.forEach { line ->
                        log("go: $line")
                        try { w.appendLine(line); w.flush() } catch (_: Exception) {}
                    }
                }
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
                if (!goAlive || !ExecUtil.waitPort(HTTP_PORT, 1500)) {
                    log("[watchdog] Go 服务掉线，重新拉起")
                    startGoServer()
                    if (!ExecUtil.waitPort(HTTP_PORT, 20_000)) log("[watchdog] Go 服务重拉失败")
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

    // ---------- 通知 ----------

    private fun startForegroundWithNotification() {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "ADBreeze 服务", NotificationManager.IMPORTANCE_LOW)
            )
        }
        val n = buildNotification("服务启动中…")
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIFICATION_ID, n)
        }
    }

    private fun updateNotification(text: String) {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        nm.notify(NOTIFICATION_ID, buildNotification(text))
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
            .addAction(0, "停止服务", stop)
            .setOngoing(true)
            .build()
    }
}
