plugins {
    id("com.android.application")
}

android {
    namespace = "com.smanx.adbreeze"
    compileSdk = 36

    // 版本号由 CI 打 release 时注入（-PappVersion=x.y.z），本地构建用默认值
    val appVersion = (project.findProperty("appVersion") as String?)?.takeIf { it.isNotBlank() } ?: "1.0.0"
    // versionCode：x.y.z → x*10000 + y*100 + z；非三段式（如 dev 构建）回落 1
    val appVersionCode = appVersion.split(".").let { p ->
        val n = p.map { it.toIntOrNull() }
        if (p.size == 3 && n.all { it != null }) n[0]!! * 10000 + n[1]!! * 100 + n[2]!! else 1
    }

    defaultConfig {
        applicationId = "com.smanx.adbreeze"
        minSdk = 26
        targetSdk = 36
        versionCode = appVersionCode
        versionName = appVersion
    }

    buildTypes {
        release {
            // 与 Flutter 项目同策略：release 先用 debug 签名，正式签名走 CI 注入
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

// AGP 9 内建 Kotlin：jvmTarget 自动对齐 compileOptions，无需 kotlin {} 配置块

dependencies {
    // 零依赖：不引入 androidx，通知/WebView 全用平台 API（minSdk 26+）
}
