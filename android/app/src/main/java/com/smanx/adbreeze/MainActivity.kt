package com.smanx.adbreeze

import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView

/**
 * 壳界面：启动 ServerService → 轮询状态 → 就绪后用 WebView 加载
 * http://127.0.0.1:9080（本机回环是浏览器安全上下文，WebCodecs 可用）。
 *
 * Basic Auth 由 onReceivedHttpAuthRequest 自动 proceed（admin/admin，
 * 与 Go 侧环境变量一致），前端同源模式完全依赖浏览器自动凭证，登录无感。
 */
class MainActivity : Activity() {

    private lateinit var root: FrameLayout
    private var statusView: TextView? = null
    private var webView: WebView? = null
    private val handler = Handler(Looper.getMainLooper())
    private var pollCount = 0

    private val poll = object : Runnable {
        override fun run() {
            when (ServerService.phase) {
                "ready" -> showWebView()
                "error" -> showError()
                else -> {
                    pollCount++
                    if (pollCount > 90) { // ~30s 仍未就绪则展示日志排查
                        showError("启动超时")
                    } else {
                        setStatus(statusText())
                        handler.postDelayed(this, 330)
                    }
                }
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        startForegroundService(Intent(this, ServerService::class.java))
        buildLoadingUi()
        handler.post(poll)
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        // WebView 随 Activity 销毁；服务继续在后台运行
        webView?.apply {
            loadUrl("about:blank")
            destroy()
        }
        webView = null
        super.onDestroy()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        val wv = webView
        if (wv != null && wv.canGoBack()) wv.goBack() else moveTaskToBack(true)
    }

    // ---------- UI ----------

    private fun statusText(): String = when (ServerService.phase) {
        "extract" -> "解压运行资产…"
        "adb" -> "启动 adb server…"
        "go" -> "启动服务…"
        else -> "启动中…"
    }

    private fun buildLoadingUi() {
        root = FrameLayout(this)
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
        }
        val bar = ProgressBar(this)
        val status = TextView(this).apply {
            text = statusText()
            textSize = 16f
            setPadding(0, 32, 0, 0)
            gravity = Gravity.CENTER
        }
        statusView = status
        box.addView(bar, ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT)
        box.addView(status, ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT)
        root.addView(box, FrameLayout.LayoutParams(
            ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT, Gravity.CENTER))
        setContentView(root)
    }

    private fun setStatus(text: String) {
        statusView?.text = text
    }

    private fun showWebView() {
        if (webView != null) return
        root.removeAllViews()
        val wv = WebView(this)
        wv.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            databaseEnabled = true
            allowContentAccess = true
            loadWithOverviewMode = true
            useWideViewPort = true
            mediaPlaybackRequiresUserGesture = false
            mixedContentMode = android.webkit.WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
        }
        wv.setBackgroundColor(Color.BLACK)
        wv.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                // 只放行本机地址，外链交给系统浏览器
                val url = request.url
                return if (url.host == "127.0.0.1" || url.host == "localhost") false else runCatching {
                    startActivity(Intent(Intent.ACTION_VIEW, url)); true
                }.getOrDefault(true)
            }

            override fun onReceivedHttpAuthRequest(
                view: WebView, handler: android.webkit.HttpAuthHandler, host: String, realm: String
            ) {
                handler.proceed("admin", "admin")
            }
        }
        wv.loadUrl("http://127.0.0.1:9080")
        webView = wv
        root.addView(wv, FrameLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
    }

    private fun showError(reason: String = "启动失败") {
        if (root.childCount > 0 && root.getChildAt(0) is WebView) return
        root.removeAllViews()
        val scroll = ScrollView(this)
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(48, 48, 48, 48)
        }
        box.addView(TextView(this).apply {
            text = "$reason：${ServerService.errorMsg ?: "未知错误"}"
            textSize = 15f
            setTextColor(Color.RED)
        })
        box.addView(TextView(this).apply {
            text = "\n服务端日志（最近 300 行）：\n\n" +
                ServerService.logLines.joinToString("\n")
            textSize = 12f
            setTextColor(Color.DKGRAY)
            setTextIsSelectable(true)
        })
        box.addView(Button(this).apply {
            text = "重试"
            setOnClickListener {
                startService(Intent(this@MainActivity, ServerService::class.java))
                ServerService.phase = "idle"
                ServerService.errorMsg = null
                buildLoadingUi()
                pollCount = 0
                handler.post(poll)
            }
        })
        scroll.addView(box)
        root.addView(scroll, FrameLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
    }
}
