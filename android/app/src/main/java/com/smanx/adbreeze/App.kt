package com.smanx.adbreeze

import android.app.Application

/**
 * 进程级初始化：让 Lang 在任何组件（Activity / Service）之前
 * 从 SharedPreferences 读回语言偏好，避免 Service 早于 Activity
 * 启动时落到默认语言。
 */
class App : Application() {
    override fun onCreate() {
        super.onCreate()
        Lang.init(getSharedPreferences(ServerService.PREFS, MODE_PRIVATE))
    }
}
